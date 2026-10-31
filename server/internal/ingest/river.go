package ingest

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/store"
)

// Deps are the collaborators of the ingest workers.
type Deps struct {
	Pool   *pgxpool.Pool
	Store  *store.Store
	Blob   *blob.Store
	Router Router
	Log    *slog.Logger
	// ReconcileMinAge defaults to one minute.
	ReconcileMinAge time.Duration
	// ReconcileInterval is how often R2 is listed for mail whose notify was lost (default 5 minutes; RECONCILE_INTERVAL).
	ReconcileInterval time.Duration
	// Extra registers additional workers (e.g. gmail thread_hydrate) on the same client.
	Extra func(*river.Workers)
	// Periodic adds scheduled jobs next to the reconcile job.
	Periodic []*river.PeriodicJob
}

// NewRiver builds a River client with the ingest workers and the periodic reconcile job (also run once at startup).
// The caller starts it (client.Start) in `serve`.
func NewRiver(d Deps) (*river.Client[pgx.Tx], error) {
	if d.Router == nil {
		d.Router = RulesRouter{}
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.ReconcileMinAge == 0 {
		d.ReconcileMinAge = time.Minute
	}
	if d.ReconcileInterval == 0 {
		d.ReconcileInterval = 5 * time.Minute
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &Worker{Store: d.Store, Blob: d.Blob, Router: d.Router, Log: d.Log})
	river.AddWorker(workers, &RerouteWorker{Store: d.Store, Log: d.Log})
	if d.Extra != nil {
		d.Extra(workers)
	}
	river.AddWorker(workers, &ReconcileWorker{Store: d.Store, Blob: d.Blob, MinAge: d.ReconcileMinAge, Log: d.Log})
	return river.NewClient(riverpgxv5.New(d.Pool), &river.Config{
		Logger:  d.Log,
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 10}},
		Workers: workers,
		PeriodicJobs: append([]*river.PeriodicJob{
			river.NewPeriodicJob(river.PeriodicInterval(d.ReconcileInterval),
				func() (river.JobArgs, *river.InsertOpts) { return ReconcileArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: true}),
		}, d.Periodic...),
	})
}

// RiverEnqueuer adapts a River client to Enqueuer.
type RiverEnqueuer struct{ Client *river.Client[pgx.Tx] }

func (e RiverEnqueuer) EnqueueIngest(r *http.Request, a IngestArgs) error {
	_, err := e.Client.Insert(r.Context(), a, nil)
	return err
}

// RequestReconcile queues a reconcile_r2 run now (the app's refresh). It is a no-op while one is already
// queued or running.
func (e RiverEnqueuer) RequestReconcile(ctx context.Context) error {
	_, err := e.Client.Insert(ctx, ReconcileArgs{}, nil)
	return err
}

// RetryIngest re-enqueues ingest_process for an existing raw object (used by the failed-mail
// retry endpoint). The earlier job for the same key has finished, so the usual by-args
// uniqueness is relaxed to "no job for this key is still pending or running".
func (e RiverEnqueuer) RetryIngest(ctx context.Context, rawKey, envFrom, envTo string, receivedAt time.Time) error {
	a := IngestArgs{Key: rawKey, EnvFrom: envFrom, EnvTo: envTo, ReceivedAt: receivedAt.UTC().Format(time.RFC3339)}
	_, err := e.Client.Insert(ctx, a, &river.InsertOpts{UniqueOpts: river.UniqueOpts{
		ByArgs: true,
		ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
			rivertype.JobStateRetryable, rivertype.JobStateScheduled},
	}})
	return err
}

// EnqueueReroute queues reroute_unrouted. It is deliberately not unique: a running job may
// have read the rules before this change, and the job is a cheap, idempotent scan.
func (e RiverEnqueuer) EnqueueReroute(ctx context.Context) error {
	_, err := e.Client.Insert(ctx, RerouteArgs{}, nil)
	return err
}
