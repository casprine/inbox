package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/riverqueue/river"

	"github.com/casprine/inbox/server/internal/events"
	"github.com/casprine/inbox/server/internal/httpapi"
)

const settingKey = "health"

// SnapshotArgs is the health_snapshot job (every 5 minutes).
type SnapshotArgs struct{}

func (SnapshotArgs) Kind() string { return "health_snapshot" }

func (SnapshotArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }

// SnapshotWorker stores the latest report, announces changes and pings the dead-man URL.
type SnapshotWorker struct {
	river.WorkerDefaults[SnapshotArgs]
	Checker    *Checker
	Events     events.Publisher // optional
	DeadmanURL string           // optional; GET after each successful snapshot
	Log        *slog.Logger
}

func (w *SnapshotWorker) Work(ctx context.Context, _ *river.Job[SnapshotArgs]) error {
	r, err := w.Checker.Run(ctx)
	if err != nil {
		return err
	}
	st := w.Checker.Store
	prev := ""
	if raw, err := st.GetSetting(ctx, settingKey); err == nil {
		var old Report
		if json.Unmarshal(raw, &old) == nil {
			prev = signature(old)
		}
	}
	if err := st.SetSetting(ctx, settingKey, r); err != nil {
		return err
	}
	if sig := signature(r); sig != prev && w.Events != nil {
		w.Events.Publish(events.New(events.TypeHealthChanged, map[string]any{"reason": "snapshot", "state": r.State}))
	}
	if w.DeadmanURL != "" {
		w.ping(ctx)
	}
	return nil
}

func (w *SnapshotWorker) ping(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, w.DeadmanURL, nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if w.Log != nil {
			w.Log.Warn("dead-man ping failed", "err", httpapi.ScrubURL(err))
		}
		return
	}
	resp.Body.Close()
}

// signature identifies the report ignoring its timestamp and wording of durations.
func signature(r Report) string {
	var b strings.Builder
	b.WriteString(r.State)
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "|%s/%v/%s", c.ID, c.BusinessID, c.State)
	}
	return b.String()
}

// PeriodicSnapshot is the 5-minute schedule.
func PeriodicSnapshot() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(5*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return SnapshotArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

// API serves GET /v1/health.
type API struct {
	Checker *Checker
	Log     *slog.Logger
}

func (a *API) Routes(r chi.Router) { r.Get("/health", a.get) }

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	rep, err := a.Checker.Run(r.Context())
	if err != nil {
		if a.Log != nil {
			a.Log.Error("health", "err", err)
		}
		httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, rep)
}
