package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/store"
)

// R2 is the queue (D-020). The Worker stores every mail as raw/YYYY/MM/<ulid>.eml and then pings the
// server; a ping can be lost (server down, Mac mini asleep, Funnel hiccup). Reconcile lists the bucket and
// ingests every object Postgres does not know yet. It runs on startup, every RECONCILE_INTERVAL, and when
// the app asks for a refresh (POST /v1/mail:refresh).
//
// To avoid listing the whole bucket every few minutes it resumes after a saved cursor: the key below which
// every object has been seen and handled. The cursor only advances over objects older than cursorLag, so
// a key that sorts earlier but became visible late (clock skew, an upload still in flight) cannot be skipped.
// Every fullScanEvery (and whenever there is no cursor) it ignores the cursor and lists and diffs everything,
// which also repairs anything the cursor logic could have missed.
const (
	rawPrefix              = "raw/"
	settingReconcileCursor = "reconcile_cursor"
	settingReconcileFull   = "reconcile_full_at"
	defaultCursorLag       = time.Hour
	defaultFullScanEvery   = 24 * time.Hour
	lookupBatch            = 500
)

// ReconcileArgs is the reconcile_r2 job (periodic, and enqueued by the app's refresh).
type ReconcileArgs struct{}

func (ReconcileArgs) Kind() string { return "reconcile_r2" }

// At most one reconcile is queued or running at a time, so refresh taps and the timer never pile up.
func (ReconcileArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 1, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
		rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
		rivertype.JobStateRetryable, rivertype.JobStateScheduled,
	}}}
}

// ReconcileWorker finds raw blobs that no message references (a lost or never-sent notify) and
// enqueues ingest_process for them.
type ReconcileWorker struct {
	river.WorkerDefaults[ReconcileArgs]
	Store  *store.Store
	Blob   *blob.Store
	Log    interface{ Info(string, ...any) } // optional
	MinAge time.Duration                     // skip objects younger than this; their notify is probably in flight
	// CursorLag is how old an object must be before the saved position moves past it (default 1h).
	CursorLag time.Duration
	// FullScanEvery forces a list-and-diff of the whole bucket this often (default 24h).
	FullScanEvery time.Duration
	Now           func() time.Time
}

func (w *ReconcileWorker) Work(ctx context.Context, _ *river.Job[ReconcileArgs]) error {
	c := river.ClientFromContext[pgx.Tx](ctx)
	n, err := w.Reconcile(ctx, func(a IngestArgs) error {
		_, err := c.Insert(ctx, a, nil)
		return err
	})
	if w.Log != nil && n > 0 {
		w.Log.Info("reconcile_r2 queued missed mail", "count", n)
	}
	return err
}

func (w *ReconcileWorker) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

func (w *ReconcileWorker) loadString(ctx context.Context, key string) string {
	raw, err := w.Store.GetSetting(ctx, key)
	if err != nil {
		return ""
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// Reconcile calls enqueue for each object Postgres lacks and returns how many it enqueued.
func (w *ReconcileWorker) Reconcile(ctx context.Context, enqueue func(IngestArgs) error) (int, error) {
	lag, every := w.CursorLag, w.FullScanEvery
	if lag == 0 {
		lag = defaultCursorLag
	}
	if every == 0 {
		every = defaultFullScanEvery
	}
	now := w.now()
	cursor := w.loadString(ctx, settingReconcileCursor)
	fullAt, _ := time.Parse(time.RFC3339, w.loadString(ctx, settingReconcileFull))
	full := cursor == "" || fullAt.IsZero() || now.Sub(fullAt) >= every
	after := cursor
	if full {
		after = ""
	}
	objs, err := w.Blob.List(ctx, rawPrefix, after)
	if err != nil {
		return 0, err
	}

	n, newCursor, blocked := 0, "", false
	var errs []error
	for start := 0; start < len(objs); start += lookupBatch {
		batch := objs[start:min(start+lookupBatch, len(objs))]
		keys := make([]string, len(batch))
		for i, o := range batch {
			keys[i] = o.Key
		}
		known, err := w.Store.RawKeysKnown(ctx, keys)
		if err != nil {
			return n, errors.Join(append(errs, err)...)
		}
		for _, o := range batch {
			if !strings.HasSuffix(o.Key, ".eml") {
				continue
			}
			age := now.Sub(o.Modified)
			if age < w.MinAge { // its notify is probably in flight: look again next time
				blocked = true
				continue
			}
			if !known[o.Key] {
				if err := w.enqueueOne(ctx, o, enqueue); err != nil {
					if !errors.Is(err, blob.ErrNotFound) {
						errs = append(errs, err)
						blocked = true
					}
					continue
				}
				n++
			}
			if !blocked && age >= lag {
				newCursor = o.Key
			}
		}
	}

	if len(errs) == 0 {
		if full {
			_ = w.Store.SetSetting(ctx, settingReconcileFull, now.Format(time.RFC3339))
		}
		if newCursor > cursor {
			_ = w.Store.SetSetting(ctx, settingReconcileCursor, newCursor)
		}
	}
	return n, errors.Join(errs...)
}

func (w *ReconcileWorker) enqueueOne(ctx context.Context, o blob.Info, enqueue func(IngestArgs) error) error {
	hi, err := w.Blob.Head(ctx, o.Key)
	if err != nil {
		return err
	}
	meta := func(k string) string {
		if v := hi.Metadata[k]; v != "" {
			return v
		}
		return hi.Metadata[k[:3]+"-"+k[4:]] // env-from spelling
	}
	return enqueue(IngestArgs{Key: o.Key, EnvFrom: meta("env_from"), EnvTo: meta("env_to"), RawSize: o.Size,
		ReceivedAt: o.Modified.UTC().Format(time.RFC3339)})
}
