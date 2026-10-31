package gmail

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/ingest"
	"github.com/casprine/inbox/server/internal/ingest/parse"
	"github.com/casprine/inbox/server/internal/store"
)

// HydrateArgs is the thread_hydrate job: import the replies you sent in one Gmail thread.
type HydrateArgs struct {
	ThreadID string `json:"thread_id" river:"unique"`
}

func (HydrateArgs) Kind() string { return "thread_hydrate" }

func (HydrateArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// HydrateWorker imports SENT messages of a thread as direction=out.
type HydrateWorker struct {
	river.WorkerDefaults[HydrateArgs]
	Svc    *Service
	Store  *store.Store
	Blob   *blob.Store
	Ingest *ingest.Worker
	Log    *slog.Logger
}

func (w *HydrateWorker) Work(ctx context.Context, job *river.Job[HydrateArgs]) error {
	n, err := w.Hydrate(ctx, job.Args.ThreadID)
	if errors.Is(err, ErrNoAccount) || errors.Is(err, ErrNeedsReauth) || errors.Is(err, ErrNotConfigured) {
		return river.JobCancel(err) // Gmail is signed out; the next lookup will queue it again
	}
	if err == nil && n > 0 && w.Log != nil {
		w.Log.Info("thread hydrated", "thread", job.Args.ThreadID, "sent_imported", n)
	}
	return err
}

// Hydrate imports the thread's sent messages that are not stored yet and returns how many it added.
func (w *HydrateWorker) Hydrate(ctx context.Context, threadID string) (int, error) {
	msgs, err := w.Svc.GetThread(ctx, threadID)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == 404 {
			return 0, nil // thread vanished
		}
		return 0, err
	}
	added := 0
	for _, m := range msgs {
		if !hasLabel(m.LabelIDs, "SENT") {
			continue
		}
		known, err := w.Store.MessageHasGmailID(ctx, m.ID)
		if err != nil {
			return added, err
		}
		if known {
			continue
		}
		raw, err := w.Svc.GetRaw(ctx, m.ID)
		if err != nil {
			return added, err
		}
		p, err := parse.Parse(raw.Raw)
		if err != nil {
			continue // unreadable sent mail is not worth failing the thread for
		}
		key := "gmail/" + m.ID + ".eml"
		if err := w.Blob.Put(ctx, key, bytesReader(raw.Raw), int64(len(raw.Raw)), nil); err != nil {
			return added, err
		}
		when := raw.Date
		if when.IsZero() {
			when = p.Date
		}
		if when.IsZero() {
			when = time.Now().UTC()
		}
		res, err := w.Ingest.Ingest(ctx, p, ingest.RawOpts{
			Key: key, ReceivedAt: when, Size: len(raw.Raw), Source: "gmail_thread", Direction: "out",
			GmailID: m.ID, GmailThread: threadID, IsRead: true,
		})
		if err != nil {
			_ = w.Blob.Delete(context.WithoutCancel(ctx), key)
			return added, err
		}
		if !res.Inserted {
			_ = w.Blob.Delete(context.WithoutCancel(ctx), key)
			continue
		}
		added++
	}
	return added, nil
}

func hasLabel(ids []string, want string) bool {
	for _, id := range ids {
		if strings.EqualFold(id, want) {
			return true
		}
	}
	return false
}

// RiverHydrate enqueues thread_hydrate jobs.
type RiverHydrate struct{ Client *river.Client[pgx.Tx] }

// EnqueueHydrate queues hydration for a Gmail thread (deduplicated by thread id while a job exists).
func (r RiverHydrate) EnqueueHydrate(ctx context.Context, threadID string) error {
	_, err := r.Client.Insert(ctx, HydrateArgs{ThreadID: threadID}, nil)
	return err
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
