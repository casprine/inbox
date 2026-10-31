package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/ids"
)

// Rerouter queues the reroute_unrouted job after rules are imported.
type Rerouter interface {
	EnqueueReroute(ctx context.Context) error
}

func (a *API) gmailProblem(w http.ResponseWriter, err error) { Problem(w, a.Svc.log(), err) }

// Problem maps an error from the token source or the Gmail API to a problem response.
func Problem(w http.ResponseWriter, log *slog.Logger, err error) {
	var ae *APIError
	switch {
	case errors.Is(err, ErrNotConfigured):
		httpapi.WriteProblem(w, http.StatusServiceUnavailable, "gmail_not_configured", "Google OAuth client is not configured on this server")
	case errors.Is(err, ErrNoAccount):
		httpapi.WriteProblem(w, http.StatusConflict, "gmail_not_connected", "Connect Gmail first")
	case errors.Is(err, ErrNeedsReauth):
		httpapi.WriteProblem(w, http.StatusConflict, "gmail_needs_reauth", "Gmail was signed out; reconnect it")
	case errors.As(err, &ae) && ae.RateLimited():
		if ae.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(ae.RetryAfter/time.Second)+1))
		}
		httpapi.WriteProblem(w, http.StatusServiceUnavailable, "gmail_rate_limited", "Gmail asked us to slow down")
	case errors.As(err, &ae) && ae.Status == http.StatusForbidden && (ae.Reason == "insufficientPermissions" || ae.Reason == "forbidden"):
		httpapi.WriteProblem(w, http.StatusForbidden, "gmail_scope_missing", "Gmail did not grant access to this; reconnect Gmail to grant it")
	case errors.As(err, &ae):
		log.Warn("gmail api error", "status", ae.Status, "reason", ae.Reason)
		httpapi.WriteProblem(w, http.StatusBadGateway, "gmail_unavailable", "Gmail returned an error")
	case errors.Is(err, context.Canceled):
		httpapi.WriteProblem(w, http.StatusServiceUnavailable, "gmail_unavailable", "request cancelled")
	default:
		log.Warn("gmail request failed", "err", err)
		httpapi.WriteProblem(w, http.StatusBadGateway, "gmail_unavailable", "Could not reach Gmail")
	}
}

type labelJSON struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

func (a *API) listLabels(w http.ResponseWriter, r *http.Request) {
	m, err := a.Svc.RefreshLabels(r.Context())
	if err != nil {
		a.gmailProblem(w, err)
		return
	}
	out := make([]labelJSON, 0, len(m))
	for _, n := range SortedNames(m) {
		out = append(out, labelJSON{n, m[n]})
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"labels": out})
}

const maxLabelName = 225

func (a *API) createLabel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > maxLabelName || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") ||
		strings.Contains(name, "//") || systemLabel[strings.ToUpper(name)] {
		httpapi.WriteProblem(w, http.StatusUnprocessableEntity, "invalid_label", "Label names must be 1-225 characters, not a Gmail system label, and may use / for nesting")
		return
	}
	existing, err := a.Svc.RefreshLabels(r.Context())
	if err != nil {
		a.gmailProblem(w, err)
		return
	}
	if id, ok := existing[name]; ok {
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"name": name, "id": id, "created": false})
		return
	}
	id, err := a.Svc.ResolveLabel(r.Context(), name)
	if err != nil {
		a.gmailProblem(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{"name": name, "id": id, "created": true})
}

func (a *API) listFilters(w http.ResponseWriter, r *http.Request) {
	fs, err := a.Svc.ListFilters(r.Context())
	if err != nil {
		a.gmailProblem(w, err)
		return
	}
	n := 0
	for _, f := range fs {
		if f.Importable && !f.AlreadyImported {
			n++
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"filters": fs, "importable": n})
}

type skippedJSON struct {
	FilterID string `json:"filter_id"`
	Reason   string `json:"reason"`
}

type importedJSON struct {
	FilterID string    `json:"filter_id"`
	RuleID   uuid.UUID `json:"rule_id"`
}

// importRules turns Gmail filters into label-only rules.
func (a *API) importRules(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FilterIDs []string `json:"filter_ids"`
	}
	if r.ContentLength != 0 {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_body", err.Error())
			return
		}
	}
	fs, err := a.Svc.ListFilters(r.Context())
	if err != nil {
		a.gmailProblem(w, err)
		return
	}
	byID := map[string]FilterInfo{}
	for _, f := range fs {
		byID[f.ID] = f
	}
	want := in.FilterIDs
	if want == nil { // omitted: everything importable
		for _, f := range fs {
			if f.Importable {
				want = append(want, f.ID)
			}
		}
	}
	imported, skipped := []importedJSON{}, []skippedJSON{}
	seen := map[string]bool{}
	for _, id := range want {
		if seen[id] {
			continue
		}
		seen[id] = true
		f, ok := byID[id]
		switch {
		case !ok:
			skipped = append(skipped, skippedJSON{id, "not_found"})
		case f.AlreadyImported:
			skipped = append(skipped, skippedJSON{id, "already_imported"})
		case !f.Importable:
			skipped = append(skipped, skippedJSON{id, *f.Reason})
		default:
			rule := f.rule
			rule.ID = ids.NewV7()
			created, err := a.Svc.Store.CreateRuleTop(r.Context(), rule)
			if err != nil {
				a.internal(w, err)
				return
			}
			imported = append(imported, importedJSON{id, created.ID})
		}
	}
	if len(imported) > 0 && a.Reroute != nil {
		if err := a.Reroute.EnqueueReroute(context.WithoutCancel(r.Context())); err != nil {
			a.Svc.log().Error("enqueue reroute after import", "err", err)
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"imported": imported, "skipped": skipped})
}
