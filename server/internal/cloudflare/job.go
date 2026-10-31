package cloudflare

import (
	"context"
	"time"

	"github.com/riverqueue/river"
)

// RoutingCheckArgs is the routing_check job (hourly).
type RoutingCheckArgs struct{}

func (RoutingCheckArgs) Kind() string { return "routing_check" }

func (RoutingCheckArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 3} }

// RoutingCheckWorker re-checks every business's catch-all.
type RoutingCheckWorker struct {
	river.WorkerDefaults[RoutingCheckArgs]
	Svc *Service
}

func (w *RoutingCheckWorker) Work(ctx context.Context, _ *river.Job[RoutingCheckArgs]) error {
	return w.Svc.CheckAll(ctx)
}

// PeriodicRoutingCheck is the hourly schedule.
func PeriodicRoutingCheck() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return RoutingCheckArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}
