package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/casprine/inbox/server/internal/store"
)

const (
	maxAttempts      = 6
	settingRateLimit = "gmail.rate_limited_until"
	defaultRetryWait = 30 * time.Second
)

// backoff is the delay after the nth failed attempt (1-based). With the first try 30s after
// routing, six tries span about 16 minutes.
var backoff = []time.Duration{60 * time.Second, 60 * time.Second, 120 * time.Second, 240 * time.Second, 480 * time.Second}

// applied is what the last successful sync left in Gmail.
type applied struct {
	Label    *string `json:"label"`
	Archived bool    `json:"archived"`
	Read     bool    `json:"read"`
}

// Engine converges Gmail to the wants stored in gmail_sync (plan §4.3). It polls for due rows,
// leases them, and runs up to Concurrency at a time through the shared 15 req/s limiter.
type Engine struct {
	Svc         *Service
	Store       *store.Store
	Log         *slog.Logger
	Concurrency int           // default 4
	Interval    time.Duration // poll interval, default 2s
	Batch       int           // rows leased per claim, default 20
	// Jitter randomises retry delays; tests set it to identity.
	Jitter func(time.Duration) time.Duration
	// OnFound runs after a message is located in Gmail (thread hydration hooks in here).
	OnFound func(ctx context.Context, gmailThreadID string)

	kickCh chan struct{}
	once   sync.Once
}

func (e *Engine) init() {
	e.once.Do(func() {
		e.kickCh = make(chan struct{}, 1)
		if e.Concurrency <= 0 {
			e.Concurrency = 4
		}
		if e.Interval <= 0 {
			e.Interval = 2 * time.Second
		}
		if e.Batch <= 0 {
			e.Batch = 20
		}
		if e.Jitter == nil {
			e.Jitter = func(d time.Duration) time.Duration {
				return d + time.Duration((rand.Float64()*0.2-0.1)*float64(d))
			}
		}
		if e.Log == nil {
			e.Log = slog.Default()
		}
	})
}

// Kick asks the engine to look for due rows now.
func (e *Engine) Kick() {
	e.init()
	select {
	case e.kickCh <- struct{}{}:
	default:
	}
}

// Run polls until ctx ends.
func (e *Engine) Run(ctx context.Context) {
	e.init()
	t := time.NewTicker(e.Interval)
	defer t.Stop()
	for {
		if _, err := e.RunOnce(ctx); err != nil && ctx.Err() == nil {
			e.Log.Warn("gmail write-back pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.kickCh:
		}
	}
}

// RunOnce processes every row that is due now and returns how many it handled.
func (e *Engine) RunOnce(ctx context.Context) (int, error) {
	e.init()
	if until := e.Svc.RateLimitedUntil(ctx); time.Now().Before(until) {
		return 0, nil
	}
	if _, err := e.Svc.AccessToken(ctx); err != nil {
		if errors.Is(err, ErrNoAccount) || errors.Is(err, ErrNeedsReauth) || errors.Is(err, ErrNotConfigured) {
			// Nothing can run; park the queue so Status reports "waiting" and reconnect resumes it.
			_, perr := e.Store.PauseGmailSync(ctx)
			return 0, perr
		}
		return 0, err
	}
	total := 0
	for ctx.Err() == nil {
		rows, err := e.Store.ClaimDueGmailSync(ctx, e.Batch, 2*time.Minute)
		if err != nil {
			return total, err
		}
		if len(rows) == 0 {
			return total, nil
		}
		jobs := make(chan store.SyncRow)
		var wg sync.WaitGroup
		for i := 0; i < e.Concurrency && i < len(rows); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for r := range jobs {
					e.process(ctx, r)
				}
			}()
		}
		for _, r := range rows {
			jobs <- r
		}
		close(jobs)
		wg.Wait()
		total += len(rows)
		if time.Now().Before(e.Svc.RateLimitedUntil(ctx)) {
			return total, nil
		}
	}
	return total, ctx.Err()
}

func (e *Engine) process(ctx context.Context, r store.SyncRow) {
	out := e.sync(ctx, r)
	if err := e.Store.FinishGmailSync(context.WithoutCancel(ctx), r, out); err != nil {
		e.Log.Error("gmail write-back: save outcome", "message", r.MessageID, "err", err)
	}
}

func (e *Engine) retryIn(attempt int) time.Duration {
	i := attempt - 1
	if i >= len(backoff) {
		i = len(backoff) - 1
	}
	if i < 0 {
		i = 0
	}
	return e.Jitter(backoff[i])
}

// retryable bumps the attempt counter and either schedules the next try or gives up with finalState.
func (e *Engine) retryable(r store.SyncRow, why error, finalState string) store.SyncOutcome {
	n := r.Attempts + 1
	msg := why.Error()
	if n >= maxAttempts {
		return store.SyncOutcome{State: finalState, Attempts: n, LastError: &msg}
	}
	return store.SyncOutcome{State: "pending", Attempts: n, NextAfter: e.retryIn(n), LastError: &msg}
}

// failure maps an error from Gmail or the token source to an outcome.
func (e *Engine) failure(ctx context.Context, r store.SyncRow, err error) store.SyncOutcome {
	if errors.Is(err, ErrNoAccount) || errors.Is(err, ErrNeedsReauth) || errors.Is(err, ErrNotConfigured) {
		msg := "Gmail is not connected"
		return store.SyncOutcome{State: "paused", Attempts: r.Attempts, LastError: &msg}
	}
	var ae *APIError
	if errors.As(err, &ae) {
		switch {
		case ae.RateLimited():
			wait := ae.RetryAfter
			if wait <= 0 {
				wait = defaultRetryWait
			}
			e.Svc.setRateLimited(ctx, time.Now().Add(wait))
			msg := "Gmail rate limit; paused"
			return store.SyncOutcome{State: "pending", Attempts: r.Attempts, NextAfter: wait, LastError: &msg}
		case ae.Status >= 500:
			return e.retryable(r, err, "failed")
		default:
			msg := err.Error()
			return store.SyncOutcome{State: "failed", Attempts: r.Attempts + 1, LastError: &msg}
		}
	}
	return e.retryable(r, err, "failed") // network error, context deadline
}

func (e *Engine) sync(ctx context.Context, r store.SyncRow) store.SyncOutcome {
	var have applied
	if len(r.Applied) > 0 {
		_ = json.Unmarshal(r.Applied, &have)
	}
	want := applied{Label: r.WantLabel, Archived: r.WantArchived && !r.LabelsOnly, Read: r.WantRead && !r.LabelsOnly}
	if want.Label != nil && *want.Label == "" {
		want.Label = nil
	}
	same := func(a, b applied) bool {
		return a.Archived == b.Archived && a.Read == b.Read && (a.Label == nil) == (b.Label == nil) && (a.Label == nil || *a.Label == *b.Label)
	}
	done := func(a applied) store.SyncOutcome {
		b, _ := json.Marshal(a)
		raw := json.RawMessage(b)
		return store.SyncOutcome{State: "synced", Applied: &raw}
	}
	if same(want, have) { // e.g. archive then undo inside the window: no Gmail call at all
		return done(want)
	}

	gmailID := ""
	if r.GmailID != nil {
		gmailID = *r.GmailID
	}
	if gmailID == "" {
		id, thread, found, err := e.Svc.FindByRFC822(ctx, r.MessageIDHdr)
		if err != nil {
			return e.failure(ctx, r, err)
		}
		if !found {
			return e.retryable(r, errors.New("message not found in Gmail yet"), "not_found")
		}
		gmailID = id
		if err := e.Store.SetMessageGmailIDs(ctx, r.MessageID, id, thread); err != nil {
			return e.failure(ctx, r, err)
		}
		if e.OnFound != nil && thread != "" {
			e.OnFound(ctx, thread)
		}
	}

	for try := 0; ; try++ {
		add, remove, err := e.labelDelta(ctx, have, want)
		if err == nil {
			err = e.Svc.Modify(ctx, gmailID, add, remove)
		}
		var ae *APIError
		switch {
		case err == nil:
			if acct, aerr := e.Svc.account(ctx); aerr == nil {
				_ = e.Store.TouchGmailWrite(ctx, acct.ID)
			}
			return done(want)
		case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
			// The message left Gmail (deleted, or the cached id was wrong): look it up again next time.
			_ = e.Store.ClearMessageGmailIDs(ctx, r.MessageID)
			return e.retryable(r, fmt.Errorf("message no longer in Gmail: %w", err), "not_found")
		case errors.As(err, &ae) && ae.Status == http.StatusBadRequest && try == 0:
			e.Svc.ForgetLabels(ctx) // a cached label id was deleted in Gmail; re-resolve once
			continue
		default:
			return e.failure(ctx, r, err)
		}
	}
}

// labelDelta computes the label ids to add and remove to move Gmail from have to want.
func (e *Engine) labelDelta(ctx context.Context, have, want applied) (add, remove []string, err error) {
	if want.Label != nil && (have.Label == nil || *have.Label != *want.Label) {
		id, err := e.Svc.ResolveLabel(ctx, *want.Label)
		if err != nil {
			return nil, nil, err
		}
		add = append(add, id)
	}
	if have.Label != nil && (want.Label == nil || *have.Label != *want.Label) {
		if id, ok := e.Svc.CachedLabelID(ctx, *have.Label); ok {
			remove = append(remove, id)
		}
	}
	switch {
	case want.Archived && !have.Archived:
		remove = append(remove, "INBOX")
	case !want.Archived && have.Archived:
		add = append(add, "INBOX")
	}
	switch {
	case want.Read && !have.Read:
		remove = append(remove, "UNREAD")
	case !want.Read && have.Read:
		add = append(add, "UNREAD")
	}
	return add, remove, nil
}

// RateLimitedUntil is when Gmail asked us to stop (zero if not limited).
func (s *Service) RateLimitedUntil(ctx context.Context) time.Time {
	raw, err := s.Store.GetSetting(ctx, settingRateLimit)
	if err != nil {
		return time.Time{}
	}
	var t time.Time
	if json.Unmarshal(raw, &t) != nil {
		return time.Time{}
	}
	return t
}

func (s *Service) setRateLimited(ctx context.Context, until time.Time) {
	cur := s.RateLimitedUntil(ctx)
	if until.Before(cur) {
		return
	}
	if err := s.Store.SetSetting(context.WithoutCancel(ctx), settingRateLimit, until.UTC()); err != nil {
		s.log().Error("save rate limit", "err", err)
		return
	}
	s.publishStatus(ctx)
}
