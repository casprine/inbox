package backfill

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/events"
	"github.com/casprine/inbox/server/internal/gmail"
	"github.com/casprine/inbox/server/internal/ingest"
	"github.com/casprine/inbox/server/internal/ingest/parse"
	"github.com/casprine/inbox/server/internal/store"
)

// Args is the backfill_page job: process pages of one backfill until done, paused or out of budget.
type Args struct {
	BackfillID uuid.UUID `json:"backfill_id" river:"unique"`
}

func (Args) Kind() string { return "backfill_page" }

func (Args) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 10, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// RiverQueue enqueues backfill jobs.
type RiverQueue struct{ Client *river.Client[pgx.Tx] }

func (r RiverQueue) EnqueueBackfill(ctx context.Context, id uuid.UUID) error {
	_, err := r.Client.Insert(ctx, Args{BackfillID: id}, nil)
	return err
}

// Worker runs backfill_page.
type Worker struct {
	river.WorkerDefaults[Args]
	Gmail  *gmail.Service
	Store  *store.Store
	Blob   *blob.Store
	Ingest *ingest.Worker
	Events events.Publisher
	Log    *slog.Logger
	// Throttle is the pause after each imported message (about 10 messages/s). Zero in tests.
	Throttle time.Duration
	// PageSize is how many ids to list per page (default 50).
	PageSize int
	// Budget is how long one job run works before it yields (River's job timeout is 1 minute). Default 40s.
	Budget time.Duration
}

// DefaultThrottle gives about MessagesPerSecond.
const DefaultThrottle = time.Second / MessagesPerSecond

func (w *Worker) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

// cursor is "pageToken|index": the index counts ids of that page already handled, so a
// rate-limit pause in the middle of a page resumes exactly where it stopped.
func parseCursor(s *string) (token string, idx int) {
	if s == nil {
		return "", 0
	}
	t, i, ok := strings.Cut(*s, "|")
	if !ok {
		return t, 0
	}
	n, _ := strconv.Atoi(i)
	return t, n
}

func cursor(token string, idx int) *string {
	if token == "" && idx == 0 {
		return nil
	}
	s := token + "|" + strconv.Itoa(idx)
	return &s
}

func (w *Worker) publish(ctx context.Context, id uuid.UUID) {
	if w.Events == nil {
		return
	}
	if b, err := w.Store.GetBackfill(ctx, id); err == nil {
		w.Events.Publish(events.New(events.TypeJobProgress, Progress(b)))
	}
}

func (w *Worker) Work(ctx context.Context, job *river.Job[Args]) error {
	id := job.Args.BackfillID
	ok, err := w.Store.MarkBackfillRunning(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		b, err := w.Store.GetBackfill(ctx, id)
		if err == nil && b.Status == "paused" && b.PausedUntil != nil {
			return river.JobSnooze(time.Until(*b.PausedUntil) + time.Second) // rate-limit wait not over
		}
		return nil // paused by the user, cancelled, finished or deleted
	}
	w.publish(ctx, id)

	budget := w.Budget
	if budget == 0 {
		budget = 40 * time.Second
	}
	deadline := time.Now().Add(budget)
	for {
		b, err := w.Store.GetBackfill(ctx, id)
		if err != nil {
			return err
		}
		if b.Status != "running" {
			return nil
		}
		done, err := w.page(ctx, b)
		if err != nil {
			return w.handle(ctx, job, b, err)
		}
		w.publish(ctx, id)
		if done {
			return nil
		}
		if time.Now().After(deadline) {
			return river.JobSnooze(time.Second)
		}
	}
}

// page handles one Gmail page; done=true when the backfill finished (or was stopped).
func (w *Worker) page(ctx context.Context, b store.Backfill) (done bool, err error) {
	size := w.PageSize
	if size <= 0 {
		size = 50
	}
	token, skip := parseCursor(b.PageToken)
	pg, err := w.Gmail.ListMessages(ctx, Query(b.Domain, b.Since), token, size)
	if err != nil {
		return false, err
	}
	var prog store.BackfillProgress
	flush := func(next *string) {
		prog.PageToken = next
		if e := w.Store.AddBackfillProgress(context.WithoutCancel(ctx), b.ID, prog); e != nil {
			w.log().Error("save backfill progress", "id", b.ID, "err", e)
		}
	}
	for i := skip; i < len(pg.IDs); i++ {
		if i%10 == 9 { // honour Pause/Cancel promptly
			cur, err := w.Store.GetBackfill(ctx, b.ID)
			if err == nil && cur.Status != "running" {
				flush(cursor(token, i))
				return true, nil
			}
		}
		out, err := w.importOne(ctx, pg.IDs[i])
		if err != nil {
			flush(cursor(token, i)) // not counted: the message is retried on resume
			return false, err
		}
		prog.Scanned++
		switch out {
		case outcomeSorted:
			prog.Imported++
			prog.Sorted++
		case outcomeUnrouted:
			prog.Imported++
			prog.Unrouted++
		default:
			prog.Skipped++
		}
		if w.Throttle > 0 && out != outcomeSkipped {
			select {
			case <-ctx.Done():
				flush(cursor(token, i+1))
				return false, ctx.Err()
			case <-time.After(w.Throttle):
			}
		}
	}
	if pg.NextPageToken != "" {
		flush(cursor(pg.NextPageToken, 0))
		return false, nil
	}
	flush(nil)
	if err := w.Store.FinishBackfill(ctx, b.ID, "done", nil); err != nil {
		return false, err
	}
	return true, nil
}

func (w *Worker) handle(ctx context.Context, job *river.Job[Args], b store.Backfill, err error) error {
	bg := context.WithoutCancel(ctx)
	var ae *gmail.APIError
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, gmail.ErrNeedsReauth) || errors.Is(err, gmail.ErrNoAccount) || errors.Is(err, gmail.ErrNotConfigured):
		_ = w.Store.SetBackfillSignedOut(bg, b.ID)
		w.publish(bg, b.ID)
		return nil
	case errors.As(err, &ae) && ae.RateLimited():
		wait := ae.RetryAfter
		if wait <= 0 {
			wait = time.Minute
		}
		_ = w.Store.SetBackfillRateLimited(bg, b.ID, time.Now().Add(wait))
		w.publish(bg, b.ID)
		return river.JobSnooze(wait + time.Second)
	}
	if job.Attempt >= job.MaxAttempts {
		msg := err.Error()
		_ = w.Store.FinishBackfill(bg, b.ID, "failed", &msg)
		w.publish(bg, b.ID)
		return nil
	}
	return err
}

type outcome int

const (
	outcomeSkipped outcome = iota
	outcomeSorted
	outcomeUnrouted
)

// importOne brings one Gmail message in. It returns an error only for conditions that stop the
// whole backfill or need a retry (rate limit, signed out, transient failures); a message that
// cannot be used is simply skipped.
func (w *Worker) importOne(ctx context.Context, gmailID string) (outcome, error) {
	known, err := w.Store.MessageHasGmailID(ctx, gmailID)
	if err != nil {
		return 0, err
	}
	if known {
		return outcomeSkipped, nil
	}
	raw, err := w.Gmail.GetRaw(ctx, gmailID)
	if err != nil {
		var ae *gmail.APIError
		if errors.As(err, &ae) && ae.Status == 404 {
			return outcomeSkipped, nil // deleted since the listing
		}
		return 0, err
	}
	p, err := parse.Parse(raw.Raw)
	if err != nil {
		return outcomeSkipped, nil
	}
	if dup, err := w.Store.HasMessageIDHdr(ctx, p.MessageID); err != nil {
		return 0, err
	} else if dup {
		// Already received live: just learn its Gmail ids so write-backs skip the lookup.
		return outcomeSkipped, w.Store.SetGmailIDByHdr(ctx, p.MessageID, raw.ID, raw.ThreadID)
	}
	key := "gmail/" + gmailID + ".eml"
	if err := w.Blob.Put(ctx, key, bytes.NewReader(raw.Raw), int64(len(raw.Raw)), nil); err != nil {
		return 0, err
	}
	when := raw.Date
	if when.IsZero() {
		when = p.Date
	}
	if when.IsZero() {
		when = time.Now().UTC()
	}
	unread := false
	for _, l := range raw.LabelIDs {
		if l == "UNREAD" {
			unread = true
		}
	}
	res, err := w.Ingest.Ingest(ctx, p, ingest.RawOpts{
		Key: key, ReceivedAt: when, Size: len(raw.Raw), Source: "backfill", GmailID: raw.ID, GmailThread: raw.ThreadID,
		IsRead: !unread, RequireBusiness: true, LabelsOnly: true,
	})
	if err != nil {
		_ = w.Blob.Delete(context.WithoutCancel(ctx), key)
		return 0, err
	}
	if !res.Inserted {
		_ = w.Blob.Delete(context.WithoutCancel(ctx), key)
		return outcomeSkipped, nil
	}
	route, err := w.Store.MessageRoute(ctx, res.MessageID)
	if err != nil {
		return 0, err
	}
	if route == "routed" {
		return outcomeSorted, nil
	}
	return outcomeUnrouted, nil
}
