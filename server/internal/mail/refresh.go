package mail

import (
	"context"
	"net/http"

	"github.com/casprine/inbox/server/internal/httpapi"
)

// Refresher asks the server to compare the raw-mail bucket (R2) with Postgres now and ingest anything
// missing (mail whose ping from the Worker never arrived). Repeated calls while a run is queued are no-ops.
type Refresher interface {
	RequestReconcile(ctx context.Context) error
}

// refresh handles POST /v1/mail:refresh. It only queues the work (202); new mail then arrives through
// the usual /v1/events stream and list endpoints.
func (a *API) refresh(w http.ResponseWriter, r *http.Request) {
	if a.Refresh == nil {
		httpapi.WriteProblem(w, http.StatusServiceUnavailable, "queue_unavailable", "job queue is not running")
		return
	}
	if err := a.Refresh.RequestReconcile(r.Context()); err != nil {
		a.log().Error("request reconcile", "err", err)
		httpapi.WriteProblem(w, http.StatusServiceUnavailable, "enqueue_failed", "could not queue the refresh")
		return
	}
	httpapi.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}
