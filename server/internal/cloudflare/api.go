package cloudflare

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/store"
)

// API serves the Cloudflare endpoints.
type API struct{ Svc *Service }

func (a *API) Routes(r chi.Router) {
	r.Get("/cloudflare/domains", a.domains)
	r.Post("/cloudflare/domains/{zone_id}:check", a.checkZone)
	r.Post("/businesses/{id}/routing:check", a.checkBusiness)
}

// Problem maps a Cloudflare error to a response. Exported for the business-create hook.
func Problem(w http.ResponseWriter, log func(string, ...any), err error) {
	switch {
	case errors.Is(err, ErrNotConfigured):
		httpapi.WriteProblem(w, http.StatusServiceUnavailable, "cloudflare_not_configured", "Cloudflare access is not set up on this server")
	case errors.Is(err, ErrUnauthorized):
		httpapi.WriteProblem(w, http.StatusBadGateway, "cloudflare_unauthorized", "Cloudflare rejected the server's API token")
	case errors.Is(err, ErrNotFound):
		httpapi.WriteProblem(w, http.StatusNotFound, "not_found", "")
	default:
		log("cloudflare request failed", "err", err)
		httpapi.WriteProblem(w, http.StatusBadGateway, "cloudflare_unavailable", "Could not reach Cloudflare")
	}
}

func (a *API) problem(w http.ResponseWriter, err error) { Problem(w, a.Svc.log().Warn, err) }

func (a *API) domains(w http.ResponseWriter, r *http.Request) {
	if !a.Svc.Configured() {
		a.problem(w, ErrNotConfigured)
		return
	}
	ds, err := a.Svc.Domains(r.Context())
	if err != nil {
		a.problem(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"domains": ds})
}

func (a *API) checkZone(w http.ResponseWriter, r *http.Request) {
	if !a.Svc.Configured() {
		a.problem(w, ErrNotConfigured)
		return
	}
	d, err := a.Svc.CheckZone(r.Context(), chi.URLParam(r, "zone_id"))
	if err != nil {
		a.problem(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, d)
}

func (a *API) checkBusiness(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpapi.WriteProblem(w, http.StatusNotFound, "not_found", "")
		return
	}
	if !a.Svc.Configured() {
		a.problem(w, ErrNotConfigured)
		return
	}
	routing, at, err := a.Svc.CheckBusiness(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpapi.WriteProblem(w, http.StatusNotFound, "not_found", "")
	case errors.Is(err, ErrNotFound):
		httpapi.WriteProblem(w, http.StatusConflict, "cloudflare_zone_unknown", "This domain is not a zone in the connected Cloudflare account")
	case err != nil:
		a.problem(w, err)
	default:
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"business_id": id, "routing": routing, "checked_at": at})
	}
}
