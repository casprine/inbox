package ingest

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/casprine/inbox/server/internal/routing"
	"github.com/casprine/inbox/server/internal/store"
)

// RerouteArgs is the reroute_unrouted job: after a rule change, re-evaluate messages that are
// currently unrouted. Messages already routed to a rule are left alone.
type RerouteArgs struct{}

func (RerouteArgs) Kind() string { return "reroute_unrouted" }

func (RerouteArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 3} }

// RerouteWorker implements reroute_unrouted.
type RerouteWorker struct {
	river.WorkerDefaults[RerouteArgs]
	Store *store.Store
	Log   *slog.Logger
}

func (w *RerouteWorker) Work(ctx context.Context, _ *river.Job[RerouteArgs]) error {
	n, err := routing.Reroute(ctx, w.Store, true)
	if err != nil {
		return err
	}
	if w.Log != nil {
		w.Log.Info("reroute_unrouted", "routed", n)
	}
	return nil
}
