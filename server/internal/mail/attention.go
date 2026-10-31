package mail

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/store"
)

// Retrier re-enqueues an ingest_process job for a stored raw object. The implementation must
// not be deduplicated against the earlier (finished) job for the same key.
type Retrier interface {
	RetryIngest(ctx context.Context, rawKey, envFrom, envTo string, receivedAt time.Time) error
}

// Errors a GmailFetcher returns for the cases the API reports specially.
var (
	// ErrNeedsGmail: Gmail is not connected (or signed out), so a copy cannot be fetched.
	ErrNeedsGmail = errors.New("mail: gmail connection needed")
	// ErrNotInGmail: Gmail has no message with the failed mail's Message-ID (or it is unknown).
	ErrNotInGmail = errors.New("mail: message not found in gmail")
)

// GmailFetcher re-imports a failed message that has no stored copy by downloading it from Gmail.
// It replaces the failed row with the stored message and returns the new message id.
type GmailFetcher interface {
	RetryFromGmail(ctx context.Context, failed store.Message) (uuid.UUID, error)
}

const attentionLimit = 200

func (a *API) attentionRoutes(r chi.Router) {
	r.Get("/attention", a.attention)
	r.Get("/attention/unrouted", a.attentionUnrouted)
	r.Get("/attention/failed", a.attentionFailed)
	r.Post("/failed/{id}:retry", a.retryFailed)
	r.Post("/failed/{id}:dismiss", a.dismissFailed)
}

type bizRefJSON struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Domain string    `json:"domain"`
}

func (a *API) attention(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts, err := a.Store.UnroutedCounts(ctx)
	if err != nil {
		a.internal(w, err)
		return
	}
	failed, err := a.Store.CountFailed(ctx)
	if err != nil {
		a.internal(w, err)
		return
	}
	refs, err := a.Store.ListBusinessRefs(ctx)
	if err != nil {
		a.internal(w, err)
		return
	}
	by := map[uuid.UUID]store.BusinessRef{}
	for _, b := range refs {
		by[b.ID] = b
	}
	type perBiz struct {
		BusinessID *uuid.UUID `json:"business_id"`
		Name       *string    `json:"name"`
		Count      int        `json:"count"`
	}
	per := []perBiz{}
	total := 0
	for _, c := range counts {
		total += c.Count
		e := perBiz{BusinessID: c.BusinessID, Count: c.Count}
		if c.BusinessID != nil {
			if b, ok := by[*c.BusinessID]; ok {
				n := b.Name
				e.Name = &n
			}
		}
		per = append(per, e)
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"unrouted": map[string]any{"count": total, "by_business": per},
		"failed":   map[string]any{"count": failed},
		"total":    total + failed,
	})
}

func (a *API) attentionUnrouted(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ms, err := a.Store.ListUnrouted(ctx, attentionLimit)
	if err != nil {
		a.internal(w, err)
		return
	}
	refs, err := a.Store.ListBusinessRefs(ctx)
	if err != nil {
		a.internal(w, err)
		return
	}
	type group struct {
		Business *bizRefJSON   `json:"business"`
		Count    int           `json:"count"`
		Messages []messageJSON `json:"messages"`
	}
	byBiz := map[uuid.UUID]*group{}
	var none *group
	for _, m := range ms {
		var g *group
		if m.BusinessID == nil {
			if none == nil {
				none = &group{Messages: []messageJSON{}}
			}
			g = none
		} else if g = byBiz[*m.BusinessID]; g == nil {
			g = &group{Messages: []messageJSON{}}
			byBiz[*m.BusinessID] = g
		}
		g.Count++
		g.Messages = append(g.Messages, toMessage(m))
	}
	groups := []group{}
	for _, b := range refs {
		if g := byBiz[b.ID]; g != nil {
			g.Business = &bizRefJSON{b.ID, b.Name, b.Domain}
			groups = append(groups, *g)
		}
	}
	if none != nil {
		groups = append(groups, *none)
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"groups": groups, "total": len(ms)})
}

type failedJSON struct {
	messageJSON
	FailureReason *string `json:"failure_reason"`
	EnvTo         *string `json:"env_to"`
	SizeBytes     *int    `json:"size_bytes"`
	HasRaw        bool    `json:"has_raw"`
}

func (a *API) attentionFailed(w http.ResponseWriter, r *http.Request) {
	ms, err := a.Store.ListFailed(r.Context(), attentionLimit)
	if err != nil {
		a.internal(w, err)
		return
	}
	out := make([]failedJSON, 0, len(ms))
	for _, m := range ms {
		out = append(out, failedJSON{messageJSON: toMessage(m), FailureReason: m.FailureReason, EnvTo: m.EnvTo,
			SizeBytes: m.SizeBytes, HasRaw: m.RawKey != nil})
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"messages": out})
}

// failedMessage loads a failed, undismissed message or writes 404.
func (a *API) failedMessage(w http.ResponseWriter, r *http.Request) (store.Message, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return store.Message{}, false
	}
	m, err := a.Store.GetMessage(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (m.State != "failed" || m.DismissedAt != nil)) {
		notFound(w)
		return m, false
	}
	if err != nil {
		a.internal(w, err)
		return m, false
	}
	return m, true
}

// retryFailed re-processes a failed message from its stored raw object. Without a raw object
// the only source is Gmail, which arrives with T25 (backfill fetch path).
func (a *API) retryFailed(w http.ResponseWriter, r *http.Request) {
	m, ok := a.failedMessage(w, r)
	if !ok {
		return
	}
	if m.RawKey == nil {
		a.retryFromGmail(w, r, m)
		return
	}
	if a.Retry == nil {
		httpapi.WriteProblem(w, http.StatusServiceUnavailable, "queue_unavailable", "job queue is not running")
		return
	}
	ctx := r.Context()
	// The failed row owns the message-id slot for this raw object; free it so the job can
	// recreate it. If enqueueing fails the row is restored.
	if err := a.Store.WithTx(ctx, func(q *store.Queries) error {
		_, err := q.DeleteFailedMessage(ctx, m.ID)
		return err
	}); err != nil {
		a.internal(w, err)
		return
	}
	if err := a.Retry.RetryIngest(ctx, *m.RawKey, deref(m.FromAddr), deref(m.EnvTo), m.ReceivedAt); err != nil {
		if _, rerr := a.Store.InsertMessage(ctx, m); rerr != nil {
			a.log().Error("restore failed message", "id", m.ID, "err", rerr)
		}
		httpapi.WriteProblem(w, http.StatusServiceUnavailable, "enqueue_failed", "could not queue job")
		return
	}
	httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{"status": "queued", "id": m.ID})
}

// retryFromGmail handles a failed message with no stored raw copy: fetch it from Gmail.
func (a *API) retryFromGmail(w http.ResponseWriter, r *http.Request, m store.Message) {
	if a.Fetch == nil {
		httpapi.WriteProblem(w, http.StatusConflict, "needs_gmail", "no stored copy; retry needs the Gmail connection")
		return
	}
	id, err := a.Fetch.RetryFromGmail(r.Context(), m)
	switch {
	case errors.Is(err, ErrNeedsGmail):
		httpapi.WriteProblem(w, http.StatusConflict, "needs_gmail", "no stored copy; connect Gmail to retry")
	case errors.Is(err, ErrNotInGmail):
		httpapi.WriteProblem(w, http.StatusConflict, "not_in_gmail", "Gmail does not have this message")
	case err != nil:
		a.log().Warn("retry from gmail", "id", m.ID, "err", err)
		httpapi.WriteProblem(w, http.StatusBadGateway, "gmail_unavailable", "Could not fetch the message from Gmail")
	default:
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "imported", "id": id})
	}
}

func (a *API) dismissFailed(w http.ResponseWriter, r *http.Request) {
	m, ok := a.failedMessage(w, r)
	if !ok {
		return
	}
	if err := a.Store.SetMessageDismissed(r.Context(), m.ID, true); err != nil {
		a.internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
