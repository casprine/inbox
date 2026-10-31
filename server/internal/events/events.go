// Package events fans server-side events out to SSE clients (GET /v1/events).
//
// Sources: Postgres NOTIFY on the mail_new channel (emitted by ingest, delivered to this hub
// via LISTEN) and in-process Publish calls from other packages (gmail, jobs, health).
// The server is single-instance (ENGINEERING_PLAN §1.3), so Publish is process-local; if the
// server is ever scaled out, route Publish through pg_notify the same way mail_new is.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/casprine/inbox/server/internal/store"
)

// Event types on the wire. The SSE `event:` field and the JSON "type" are the same string.
const (
	TypeMailNew       = "mail.new"
	TypeGmailStatus   = "gmail.status"
	TypeJobProgress   = "job.progress"
	TypeHealthChanged = "health.changed"
)

// Event is the JSON envelope: {"type": "...", ...fields}. Fields are flattened beside type.
type Event struct {
	Type   string
	Fields map[string]any
}

// MarshalJSON flattens Fields next to "type" (a "type" key in Fields is ignored).
func (e Event) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, len(e.Fields)+1)
	for k, v := range e.Fields {
		m[k] = v
	}
	m["type"] = e.Type
	return json.Marshal(m)
}

// New builds an event with the given fields.
func New(typ string, fields map[string]any) Event { return Event{Type: typ, Fields: fields} }

// Publisher is what other packages depend on to emit events.
type Publisher interface{ Publish(Event) }

const subBuffer = 64

// Hub broadcasts events to subscribers.
type Hub struct {
	Pool      *pgxpool.Pool // for LISTEN; nil disables the Postgres source
	Store     *store.Store  // to enrich mail_new notifications
	Log       *slog.Logger
	Heartbeat time.Duration // default 20s

	mu     sync.Mutex
	subs   map[*sub]struct{}
	closed bool
	nextID uint64
}

type sub struct {
	ch   chan frame
	dead bool
}

type frame struct {
	id   uint64
	data []byte
	typ  string
}

// NewHub returns a hub; call Run in a goroutine.
func NewHub(pool *pgxpool.Pool, st *store.Store, log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{Pool: pool, Store: st, Log: log, subs: map[*sub]struct{}{}}
}

func (h *Hub) heartbeat() time.Duration {
	if h.Heartbeat > 0 {
		return h.Heartbeat
	}
	return 20 * time.Second
}

// Publish delivers e to all current subscribers. It never blocks: a subscriber whose buffer is
// full is dropped (its stream ends and the client reconnects and refetches).
func (h *Hub) Publish(e Event) {
	data, err := json.Marshal(e)
	if err != nil {
		h.Log.Error("events: marshal", "type", e.Type, "err", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.nextID++
	f := frame{id: h.nextID, data: data, typ: e.Type}
	for s := range h.subs {
		select {
		case s.ch <- f:
		default:
			h.dropLocked(s)
		}
	}
}

func (h *Hub) dropLocked(s *sub) {
	if !s.dead {
		s.dead = true
		close(s.ch)
	}
	delete(h.subs, s)
}

func (h *Hub) subscribe() (*sub, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, false
	}
	s := &sub{ch: make(chan frame, subBuffer)}
	h.subs[s] = struct{}{}
	return s, true
}

func (h *Hub) unsubscribe(s *sub) {
	h.mu.Lock()
	h.dropLocked(s)
	h.mu.Unlock()
}

// Close ends every stream and refuses new ones. Run calls it when its context ends.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for s := range h.subs {
		h.dropLocked(s)
	}
}

// Subscribers reports the number of live streams (for tests and health).
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Run LISTENs on mail_new until ctx ends, reconnecting with backoff, then closes the hub.
func (h *Hub) Run(ctx context.Context) {
	defer h.Close()
	if h.Pool == nil {
		<-ctx.Done()
		return
	}
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := h.listen(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > 30*time.Second {
			backoff = time.Second
		}
		h.Log.Warn("events: listener stopped, reconnecting", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (h *Hub) listen(ctx context.Context) error {
	conn, err := pgx.ConnectConfig(ctx, h.Pool.Config().ConnConfig)
	if err != nil {
		return err
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = conn.Close(cctx)
	}()
	if _, err := conn.Exec(ctx, "LISTEN mail_new"); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		h.onMailNew(ctx, n.Payload)
	}
}

func (h *Hub) onMailNew(ctx context.Context, payload string) {
	fields := map[string]any{"message_id": payload}
	if id, err := uuid.Parse(payload); err == nil && h.Store != nil {
		m, err := h.Store.GetMessage(ctx, id)
		switch {
		case err == nil:
			fields["business_id"] = m.BusinessID
			fields["route"] = m.Route
			fields["state"] = m.State
			fields["received_at"] = m.ReceivedAt
		case errors.Is(err, store.ErrNotFound):
		default:
			h.Log.Warn("events: lookup new message", "id", id, "err", err)
		}
	}
	h.Publish(New(TypeMailNew, fields))
}

// ServeHTTP is GET /v1/events: a text/event-stream of Events with periodic heartbeat comments.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-store")
	hd.Set("Connection", "keep-alive")
	hd.Set("X-Accel-Buffering", "no")
	s, ok := h.subscribe()
	if !ok {
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	defer h.unsubscribe(s)
	_ = rc.SetWriteDeadline(time.Time{}) // streams outlive the server's write timeout
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("retry: 3000\n: connected\n\n")); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		return
	}
	tick := time.NewTicker(h.heartbeat())
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case f, open := <-s.ch:
			if !open {
				return
			}
			if _, err := w.Write([]byte("id: " + strconv.FormatUint(f.id, 10) + "\nevent: " + f.typ + "\ndata: " + string(f.data) + "\n\n")); err != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		case <-tick.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		}
	}
}
