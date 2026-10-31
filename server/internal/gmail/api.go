package gmail

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/auth"
	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/store"
)

// API serves the Gmail endpoints.
type API struct {
	Svc *Service
	// Engine is kicked after a retry so the row runs now; nil in tests that call RunOnce themselves.
	Engine *Engine
	// Reroute is nil when the job queue is not running.
	Reroute Rerouter
}

// PublicRoutes mounts the OAuth callback (no bearer: it is bound by the state).
func (a *API) PublicRoutes(r chi.Router) {
	r.Get("/oauth/google/callback", a.callback)
}

// Routes mounts the authenticated endpoints on the /v1 group.
func (a *API) Routes(r chi.Router) {
	r.Post("/gmail/connect", a.connect)
	r.Get("/gmail/status", a.status)
	r.Post("/gmail:disconnect", a.disconnect)
	r.Post("/messages/{id}/gmail:retry", a.retry)
	r.Get("/gmail/labels", a.listLabels)
	r.Post("/gmail/labels", a.createLabel)
	r.Get("/gmail/filters", a.listFilters)
	r.Post("/rules:import", a.importRules)
}

func (a *API) retry(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpapi.WriteProblem(w, http.StatusNotFound, "not_found", "")
		return
	}
	if _, err := a.Svc.Store.GetGmailAccount(r.Context()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpapi.WriteProblem(w, http.StatusConflict, "gmail_not_connected", "Connect Gmail to retry")
			return
		}
		a.internal(w, err)
		return
	}
	found, retried, err := a.Svc.Store.RetryGmailSync(r.Context(), id)
	switch {
	case err != nil:
		a.internal(w, err)
	case !found:
		httpapi.WriteProblem(w, http.StatusNotFound, "not_found", "")
	case !retried:
		httpapi.WriteProblem(w, http.StatusConflict, "gmail_nothing_to_retry", "Only failed or not-found Gmail syncs can be retried")
	default:
		if a.Engine != nil {
			a.Engine.Kick()
		}
		httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{"status": "queued", "id": id})
	}
}

func (a *API) internal(w http.ResponseWriter, err error) {
	a.Svc.log().Error("gmail api", "err", err)
	httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
}

func (a *API) connect(w http.ResponseWriter, r *http.Request) {
	dev, _ := auth.DeviceFrom(r.Context())
	var devID *uuid.UUID
	if dev.ID != uuid.Nil {
		devID = &dev.ID
	}
	u, err := a.Svc.Connect(r.Context(), devID)
	switch {
	case errors.Is(err, ErrNotConfigured):
		httpapi.WriteProblem(w, http.StatusServiceUnavailable, "gmail_not_configured", "Google OAuth client is not configured on this server")
	case err != nil:
		a.internal(w, err)
	default:
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{
			"auth_url": u, "callback_scheme": "inbox", "expires_in": int(stateTTL / time.Second),
		})
	}
}

func (a *API) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res := a.Svc.HandleCallback(r.Context(), q.Get("code"), q.Get("state"), q.Get("error"))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, res.Redirect(), http.StatusFound)
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	st, err := a.Svc.Status(r.Context())
	if err != nil {
		a.internal(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, statusJSON(st, a.Svc.Configured()))
}

func statusJSON(st Status, configured bool) map[string]any {
	return map[string]any{
		"state": st.State, "email": st.Email, "connected_at": st.ConnectedAt,
		"last_write_at": st.LastWriteAt, "waiting": st.Waiting, "configured": configured,
		"rate_limited_until": st.RateLimitedUntil,
	}
}

func (a *API) disconnect(w http.ResponseWriter, r *http.Request) {
	ok, err := a.Svc.Disconnect(r.Context())
	switch {
	case err != nil:
		a.internal(w, err)
	case !ok:
		httpapi.WriteProblem(w, http.StatusConflict, "gmail_not_connected", "")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
