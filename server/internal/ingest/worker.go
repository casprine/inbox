package ingest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/riverqueue/river"

	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest/parse"
	"github.com/casprine/inbox/server/internal/store"
)

const maxSearchBody = 200 << 10

// IngestArgs is the ingest_process job. Key (or FailID for store_failed) identifies the
// unit of work: the job is unique on those fields, so repeated notifies are no-ops.
type IngestArgs struct {
	Key        string `json:"key,omitempty" river:"unique"`
	FailID     string `json:"fail_id,omitempty" river:"unique"`
	Reason     string `json:"reason,omitempty"`
	EnvFrom    string `json:"env_from,omitempty"`
	EnvTo      string `json:"env_to,omitempty"`
	RawSize    int64  `json:"raw_size,omitempty"`
	ReceivedAt string `json:"received_at,omitempty"`
	MessageID  string `json:"message_id,omitempty"`
}

func (IngestArgs) Kind() string { return "ingest_process" }

func (IngestArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// Worker implements ingest_process.
type Worker struct {
	river.WorkerDefaults[IngestArgs]
	Store  *store.Store
	Blob   *blob.Store
	Router Router
	Log    *slog.Logger
}

func (w *Worker) Work(ctx context.Context, job *river.Job[IngestArgs]) error {
	if job.Args.Reason == "store_failed" {
		return w.workFailed(ctx, job.Args)
	}
	return w.workRaw(ctx, job.Args)
}

func receivedAt(s string, fallback time.Time) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	return fallback
}

func (w *Worker) workFailed(ctx context.Context, a IngestArgs) error {
	id := "failed:" + a.FailID
	reason := "store_failed"
	env := strings.ToLower(a.EnvTo)
	from := strings.ToLower(a.EnvFrom)
	m := store.Message{
		ID: ids.NewV7(), Source: "edge", State: "failed", FailureReason: &reason,
		MessageIDHdr: id, EnvTo: &env, FromAddr: &from, ThreadKey: id, Route: "unrouted",
		ReceivedAt: receivedAt(a.ReceivedAt, time.Now().UTC()),
	}
	return w.Store.WithTx(ctx, func(q *store.Queries) error {
		ok, err := q.InsertMessage(ctx, m)
		if err != nil {
			return err
		}
		if !ok {
			return q.AppendIngestLog(ctx, id, nil, "info", "duplicate notification ignored")
		}
		if h := strings.Trim(strings.TrimSpace(a.MessageID), "<>"); h != "" {
			if err := q.SetOrigMessageID(ctx, m.ID, h); err != nil {
				return err
			}
		}
		if err := q.AppendIngestLog(ctx, id, &m.ID, "error", "worker could not store the raw message (store_failed)"); err != nil {
			return err
		}
		return q.NotifyMailNew(ctx, m.ID)
	})
}

func (w *Worker) workRaw(ctx context.Context, a IngestArgs) error {
	rc, err := w.Blob.Get(ctx, a.Key)
	if err != nil {
		return err // not found yet or transient: retry with backoff
	}
	raw, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return fmt.Errorf("read %s: %w", a.Key, err)
	}
	now := time.Now().UTC()
	rcv := receivedAt(a.ReceivedAt, now)
	size := len(raw)
	sizeLine := "received " + humanSize(size)

	p, perr := parse.Parse(raw)
	if perr != nil {
		reason := "parse_failed"
		env := strings.ToLower(a.EnvTo)
		from := strings.ToLower(a.EnvFrom)
		hdr := "raw:" + a.Key
		m := store.Message{
			ID: ids.NewV7(), Source: "edge", State: "failed", FailureReason: &reason, MessageIDHdr: hdr,
			EnvTo: &env, FromAddr: &from, ThreadKey: hdr, Route: "unrouted", ReceivedAt: rcv, SizeBytes: &size, RawKey: &a.Key,
		}
		return w.Store.WithTx(ctx, func(q *store.Queries) error {
			ok, err := q.InsertMessage(ctx, m)
			if err != nil {
				return err
			}
			if !ok {
				return q.AppendIngestLog(ctx, a.Key, nil, "info", "duplicate ignored")
			}
			_ = q.AppendIngestLog(ctx, a.Key, &m.ID, "info", sizeLine)
			if err := q.AppendIngestLog(ctx, a.Key, &m.ID, "error", "parse failed: "+perr.Error()); err != nil {
				return err
			}
			return q.NotifyMailNew(ctx, m.ID)
		})
	}

	_, err = w.Ingest(ctx, p, RawOpts{Key: a.Key, EnvTo: a.EnvTo, ReceivedAt: rcv, Size: size, Source: "edge"})
	return err
}

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// Process runs one raw-object ingest synchronously (used by the demo seeder; the River
// job calls the same code).
func (w *Worker) Process(ctx context.Context, a IngestArgs) error { return w.workRaw(ctx, a) }

// ProcessFailed records a store_failed notification synchronously (used by the demo seeder).
func (w *Worker) ProcessFailed(ctx context.Context, a IngestArgs) error { return w.workFailed(ctx, a) }
