package backfill

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/gmail"
	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/store"
)

// API serves /v1/backfills.
type API struct {
	Svc *Service
	Now func() time.Time
}

func (a *API) Routes(r chi.Router) {
	r.Get("/backfills:estimate", a.estimate)
	r.Get("/backfills", a.list)
	r.Post("/backfills", a.create)
	r.Post("/backfills/{id}:pause", a.control("pause"))
	r.Post("/backfills/{id}:resume", a.control("resume"))
	r.Post("/backfills/{id}:cancel", a.control("cancel"))
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

type backfillJSON struct {
	ID           uuid.UUID  `json:"id"`
	BusinessID   uuid.UUID  `json:"business_id"`
	BusinessName string     `json:"business_name"`
	Domain       string     `json:"domain"`
	Since        string     `json:"since"`
	Status       string     `json:"status"`
	Estimated    *int       `json:"estimated"`
	Scanned      int        `json:"scanned"`
	Imported     int        `json:"imported"`
	Sorted       int        `json:"sorted"`
	Unrouted     int        `json:"unrouted"`
	Skipped      int        `json:"skipped"`
	PausedUntil  *time.Time `json:"paused_until"`
	PauseReason  *string    `json:"pause_reason"`
	LastError    *string    `json:"last_error"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}

func toJSON(b store.Backfill) backfillJSON {
	j := backfillJSON{ID: b.ID, BusinessID: b.BusinessID, BusinessName: b.BusinessName, Domain: b.Domain,
		Since: b.Since.Format("2006-01-02"), Status: b.Status, Estimated: b.Estimated, Scanned: b.Scanned, Imported: b.Imported,
		Sorted: b.Sorted, Unrouted: b.Unrouted, Skipped: b.Skipped, PausedUntil: b.PausedUntil,
		CreatedAt: b.CreatedAt, StartedAt: b.StartedAt, FinishedAt: b.FinishedAt}
	switch {
	case b.Status == "paused" && b.LastError != nil:
		j.PauseReason = b.LastError // rate_limited | gmail_signed_out
	case b.Status == "paused":
		r := "user"
		j.PauseReason = &r
	case b.Status == "failed":
		j.LastError = b.LastError
	}
	return j
}

func (a *API) problem(w http.ResponseWriter, err error) {
	var active ErrActive
	switch {
	case errors.As(err, &active):
		httpapi.WriteProblem(w, http.StatusConflict, "backfill_active", "Already importing older mail for "+active.Domain)
	case errors.Is(err, ErrNotFound), errors.Is(err, store.ErrNotFound):
		httpapi.WriteProblem(w, http.StatusNotFound, "not_found", "")
	case errors.Is(err, ErrNoQueue):
		httpapi.WriteProblem(w, http.StatusServiceUnavailable, "queue_unavailable", "job queue is not running")
	case errors.Is(err, ErrBadState):
		httpapi.WriteProblem(w, http.StatusConflict, "backfill_bad_state", "That isn't possible in the import's current state")
	default:
		gmail.Problem(w, a.Svc.log(), err)
	}
}

// parseRange reads days or since (exactly one) and returns the start date.
func (a *API) parseRange(days, since string) (time.Time, bool) {
	today := a.now().UTC().Truncate(24 * time.Hour)
	switch {
	case days != "" && since == "":
		n, err := strconv.Atoi(days)
		if err != nil || n < 1 || n > 3650 {
			return time.Time{}, false
		}
		return today.AddDate(0, 0, -n), true
	case since != "" && days == "":
		t, err := time.Parse("2006-01-02", since)
		if err != nil || t.After(today) || t.Year() < 2004 {
			return time.Time{}, false
		}
		return t, true
	}
	return time.Time{}, false
}

func parseIDs(csv string) ([]uuid.UUID, bool) {
	var out []uuid.UUID
	for _, s := range strings.Split(csv, ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

func (a *API) estimate(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, ok := a.parseRange(q.Get("days"), q.Get("since"))
	if !ok {
		httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_range", "give days (1-3650) or since (YYYY-MM-DD), not both")
		return
	}
	biz, ok := parseIDs(q.Get("businesses"))
	if !ok {
		httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_business", "businesses must be a comma-separated list of ids")
		return
	}
	est, total, err := a.Svc.Estimate(r.Context(), since, biz)
	if err != nil {
		a.problem(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"since": since.Format("2006-01-02"), "businesses": est, "total": total, "eta_seconds": total / MessagesPerSecond,
	})
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Days        *int        `json:"days"`
		Since       string      `json:"since"`
		BusinessIDs []uuid.UUID `json:"business_ids"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	days := ""
	if in.Days != nil {
		days = strconv.Itoa(*in.Days)
	}
	since, ok := a.parseRange(days, in.Since)
	if !ok {
		httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_range", "give days (1-3650) or since (YYYY-MM-DD), not both")
		return
	}
	bs, err := a.Svc.Create(r.Context(), since, in.BusinessIDs)
	if err != nil {
		a.problem(w, err)
		return
	}
	out := make([]backfillJSON, 0, len(bs))
	for _, b := range bs {
		out = append(out, toJSON(b))
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{"backfills": out})
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	bs, err := a.Svc.Store.ListBackfills(r.Context())
	if err != nil {
		a.Svc.log().Error("list backfills", "err", err)
		httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
		return
	}
	out := make([]backfillJSON, 0, len(bs))
	for _, b := range bs {
		out = append(out, toJSON(b))
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"backfills": out})
}

func (a *API) control(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			httpapi.WriteProblem(w, http.StatusNotFound, "not_found", "")
			return
		}
		var b store.Backfill
		switch op {
		case "pause":
			b, err = a.Svc.Pause(r.Context(), id)
		case "resume":
			b, err = a.Svc.Resume(r.Context(), id)
		default:
			b, err = a.Svc.Cancel(r.Context(), id)
		}
		if err != nil {
			a.problem(w, err)
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, toJSON(b))
	}
}
