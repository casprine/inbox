// Package backfill imports older Gmail mail for the businesses you choose: estimate, create,
// pause/resume/cancel, and the page-by-page worker.
package backfill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/events"
	"github.com/casprine/inbox/server/internal/gmail"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/store"
)

// ErrActive means the business already has an unfinished backfill.
type ErrActive struct{ Domain string }

func (e ErrActive) Error() string { return "backfill already running for " + e.Domain }

// ErrNotFound / ErrBadState are returned by the controls.
var (
	ErrNotFound = errors.New("backfill: not found")
	ErrBadState = errors.New("backfill: not in a state that allows this")
	// ErrNoQueue: the job queue is not running (object storage is not configured).
	ErrNoQueue = errors.New("backfill: job queue not running")
)

// MessagesPerSecond is the import pace the ETA assumes.
const MessagesPerSecond = 10

// Enqueuer starts the backfill_page job for a backfill.
type Enqueuer interface {
	EnqueueBackfill(ctx context.Context, id uuid.UUID) error
}

// Service is the control side of backfills.
type Service struct {
	Store  *store.Store
	Gmail  *gmail.Service
	Queue  Enqueuer
	Events events.Publisher
	Log    *slog.Logger
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Query is the Gmail search for one business: mail addressed to its domain since a date,
// without your own sent mail and drafts.
func Query(domain string, since time.Time) string {
	return fmt.Sprintf("to:@%s after:%s -in:sent -in:drafts", strings.ToLower(domain), since.UTC().Format("2006/01/02"))
}

// BusinessEstimate is one row of OB09's "About N messages".
type BusinessEstimate struct {
	BusinessID uuid.UUID `json:"business_id"`
	Name       string    `json:"name"`
	Domain     string    `json:"domain"`
	Estimate   int       `json:"estimate"`
}

func (s *Service) pickBusinesses(ctx context.Context, want []uuid.UUID) ([]store.Business, error) {
	all, err := s.Store.ListBusinesses(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.Business
	if len(want) == 0 {
		for _, b := range all {
			if b.Enabled {
				out = append(out, b)
			}
		}
		return out, nil
	}
	by := map[uuid.UUID]store.Business{}
	for _, b := range all {
		by[b.ID] = b
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range want {
		b, ok := by[id]
		if !ok {
			return nil, store.ErrNotFound
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, b)
		}
	}
	return out, nil
}

// Estimate asks Gmail how much mail each business has since the date (resultSizeEstimate).
func (s *Service) Estimate(ctx context.Context, since time.Time, businesses []uuid.UUID) ([]BusinessEstimate, int, error) {
	bs, err := s.pickBusinesses(ctx, businesses)
	if err != nil {
		return nil, 0, err
	}
	out := make([]BusinessEstimate, 0, len(bs))
	total := 0
	for _, b := range bs {
		p, err := s.Gmail.ListMessages(ctx, Query(b.Domain, since), "", 1)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, BusinessEstimate{b.ID, b.Name, b.Domain, p.Estimate})
		total += p.Estimate
	}
	return out, total, nil
}

// Create queues one backfill per business. It fails as a whole when a business is already being
// backfilled, so the app never half-starts.
func (s *Service) Create(ctx context.Context, since time.Time, businesses []uuid.UUID) ([]store.Backfill, error) {
	if s.Queue == nil {
		return nil, ErrNoQueue
	}
	bs, err := s.pickBusinesses(ctx, businesses)
	if err != nil {
		return nil, err
	}
	if _, err := s.Gmail.AccessToken(ctx); err != nil {
		return nil, err
	}
	est, _, err := s.Estimate(ctx, since, businesses)
	if err != nil {
		return nil, err
	}
	estimate := map[uuid.UUID]int{}
	for _, e := range est {
		estimate[e.BusinessID] = e.Estimate
	}
	var created []uuid.UUID
	err = s.Store.WithTx(ctx, func(q *store.Queries) error {
		active, err := q.ActiveBackfillBusinesses(ctx)
		if err != nil {
			return err
		}
		for _, b := range bs {
			if active[b.ID] {
				return ErrActive{b.Domain}
			}
			id := ids.NewV7()
			if err := q.CreateBackfill(ctx, id, b.ID, since, estimate[b.ID]); err != nil {
				return err
			}
			created = append(created, id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]store.Backfill, 0, len(created))
	for _, id := range created {
		if err := s.Queue.EnqueueBackfill(ctx, id); err != nil {
			e := "enqueue failed"
			_ = s.Store.FinishBackfill(context.WithoutCancel(ctx), id, "failed", &e)
		}
		b, err := s.Store.GetBackfill(ctx, id)
		if err != nil {
			return nil, err
		}
		s.publish(b)
		out = append(out, b)
	}
	return out, nil
}

func (s *Service) publish(b store.Backfill) {
	if s.Events == nil {
		return
	}
	s.Events.Publish(events.New(events.TypeJobProgress, Progress(b)))
}

// Progress is the job.progress event body for a backfill.
func Progress(b store.Backfill) map[string]any {
	return map[string]any{
		"job": "backfill", "backfill_id": b.ID, "business_id": b.BusinessID, "status": b.Status,
		"estimated": b.Estimated, "scanned": b.Scanned, "imported": b.Imported, "sorted": b.Sorted,
		"unrouted": b.Unrouted, "skipped": b.Skipped, "paused_until": b.PausedUntil,
	}
}

func (s *Service) load(ctx context.Context, id uuid.UUID) (store.Backfill, error) {
	b, err := s.Store.GetBackfill(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return b, ErrNotFound
	}
	return b, err
}

// Pause stops a running or queued backfill; the worker notices at its next check.
func (s *Service) Pause(ctx context.Context, id uuid.UUID) (store.Backfill, error) {
	if _, err := s.load(ctx, id); err != nil {
		return store.Backfill{}, err
	}
	if ok, err := s.Store.PauseBackfill(ctx, id); err != nil {
		return store.Backfill{}, err
	} else if !ok {
		return store.Backfill{}, ErrBadState
	}
	b, err := s.load(ctx, id)
	s.publish(b)
	return b, err
}

// Resume requeues a paused backfill (also used to skip a rate-limit wait).
func (s *Service) Resume(ctx context.Context, id uuid.UUID) (store.Backfill, error) {
	if s.Queue == nil {
		return store.Backfill{}, ErrNoQueue
	}
	if _, err := s.load(ctx, id); err != nil {
		return store.Backfill{}, err
	}
	if _, err := s.Gmail.AccessToken(ctx); err != nil {
		return store.Backfill{}, err
	}
	if ok, err := s.Store.ResumeBackfill(ctx, id); err != nil {
		return store.Backfill{}, err
	} else if !ok {
		return store.Backfill{}, ErrBadState
	}
	if err := s.Queue.EnqueueBackfill(ctx, id); err != nil {
		return store.Backfill{}, err
	}
	b, err := s.load(ctx, id)
	s.publish(b)
	return b, err
}

// Cancel ends a backfill; what was imported stays.
func (s *Service) Cancel(ctx context.Context, id uuid.UUID) (store.Backfill, error) {
	if _, err := s.load(ctx, id); err != nil {
		return store.Backfill{}, err
	}
	if ok, err := s.Store.CancelBackfill(ctx, id); err != nil {
		return store.Backfill{}, err
	} else if !ok {
		return store.Backfill{}, ErrBadState
	}
	b, err := s.load(ctx, id)
	s.publish(b)
	return b, err
}

// ResumeSignedOut requeues backfills that paused because Gmail was signed out. Called on reconnect.
func (s *Service) ResumeSignedOut(ctx context.Context) {
	if s.Queue == nil {
		return
	}
	ids, err := s.Store.ResumeSignedOutBackfills(ctx)
	if err != nil {
		s.log().Error("resume backfills", "err", err)
		return
	}
	for _, id := range ids {
		if err := s.Queue.EnqueueBackfill(ctx, id); err != nil {
			s.log().Error("enqueue backfill", "id", id, "err", err)
		}
	}
}
