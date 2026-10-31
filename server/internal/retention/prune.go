package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/casprine/inbox/server/internal/store"
)

// BlobDeleter is the part of the blob store the pruner needs.
type BlobDeleter interface {
	DeleteMany(ctx context.Context, keys []string) error
}

const (
	batchSize       = 200
	idempotencyKeep = 24 // hours
	orphanLogKeep   = 30 * 24 * time.Hour
)

// Result says what a prune run removed.
type Result struct {
	Messages        int   `json:"messages"`
	Blobs           int   `json:"blobs"`
	IdempotencyKeys int64 `json:"idempotency_keys"`
	LogLines        int64 `json:"log_lines"`
}

// Pruner applies retention and clears housekeeping tables.
type Pruner struct {
	Store *store.Store
	Blob  BlobDeleter // nil: blobs are left (rows are still deleted)
	Log   *slog.Logger
	Now   func() time.Time
}

func (p *Pruner) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

func (p *Pruner) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// Run deletes mail older than retention_days (with its blobs), then expired idempotency keys,
// stale OAuth states and orphaned log lines.
func (p *Pruner) Run(ctx context.Context) (Result, error) {
	var res Result
	prefs, err := Load(ctx, p.Store)
	if err != nil {
		return res, err
	}
	now := p.now()
	if prefs.RetentionDays != nil {
		cutoff := now.AddDate(0, 0, -*prefs.RetentionDays)
		n, b, err := p.deleteMessages(ctx, &cutoff)
		res.Messages, res.Blobs = n, b
		if err != nil {
			return res, err
		}
	}
	if res.IdempotencyKeys, err = p.Store.DeleteExpiredIdempotencyKeys(ctx, idempotencyKeep); err != nil {
		return res, err
	}
	if _, err = p.Store.DeleteExpiredGmailOAuthStates(ctx); err != nil {
		return res, err
	}
	if res.LogLines, err = p.Store.DeleteOrphanIngestLog(ctx, now.Add(-orphanLogKeep)); err != nil {
		return res, err
	}
	if res.Messages > 0 {
		p.log().Info("retention prune", "messages", res.Messages, "blobs", res.Blobs)
	}
	return res, nil
}

// deleteMessages removes messages in batches (all when cutoff is nil) and their blobs.
func (p *Pruner) deleteMessages(ctx context.Context, cutoff *time.Time) (messages, blobs int, err error) {
	for {
		n, keys, err := p.Store.PruneMessages(ctx, cutoff, batchSize)
		if err != nil {
			return messages, blobs, err
		}
		if n == 0 {
			return messages, blobs, nil
		}
		messages += n
		if p.Blob != nil && len(keys) > 0 {
			if err := p.Blob.DeleteMany(ctx, keys); err != nil {
				// The rows are gone; the orphaned objects are removed by the next purge or reconcile sweep.
				p.log().Warn("prune: blob delete failed", "keys", len(keys), "err", err)
			} else {
				blobs += len(keys)
			}
		}
	}
}

// PruneArgs is the daily prune job.
type PruneArgs struct{}

func (PruneArgs) Kind() string { return "prune" }

func (PruneArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 3} }

// PruneWorker runs the Pruner.
type PruneWorker struct {
	river.WorkerDefaults[PruneArgs]
	Pruner *Pruner
}

func (w *PruneWorker) Work(ctx context.Context, _ *river.Job[PruneArgs]) error {
	_, err := w.Pruner.Run(ctx)
	return err
}

// dailyAt schedules a job at a fixed UTC hour every day.
type dailyAt struct{ hour int }

func (d dailyAt) Next(t time.Time) time.Time {
	t = t.UTC()
	n := time.Date(t.Year(), t.Month(), t.Day(), d.hour, 0, 0, 0, time.UTC)
	if !n.After(t) {
		n = n.AddDate(0, 0, 1)
	}
	return n
}

// PeriodicPrune runs every day at 03:00 UTC.
func PeriodicPrune() *river.PeriodicJob {
	return river.NewPeriodicJob(dailyAt{3},
		func() (river.JobArgs, *river.InsertOpts) { return PruneArgs{}, nil }, nil)
}

// NextRun exposes the schedule for tests.
func NextRun(after time.Time) time.Time { return dailyAt{3}.Next(after) }
