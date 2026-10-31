package alfather

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/casprine/inbox/server/internal/crypto"
	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/store"
)

// ConnectedWithin is how recently Alfather must have called for the status to say connected.
const ConnectedWithin = 5 * time.Minute

// Scope is the only permission a pairing grants.
const Scope = "mail.read"

// API serves the pairing endpoints (app side) and the MCP endpoint (Alfather side).
type API struct {
	Store *store.Store
	// BaseURL is the public origin of the server (PUBLIC_BASE_URL); empty falls back to the request's host.
	BaseURL string
	Log     *slog.Logger
	Now     func() time.Time
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *API) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}

// Routes mounts the app-facing endpoints on the authenticated /v1 group.
func (a *API) Routes(r chi.Router) {
	r.Post("/alfather/pairings", a.pair)
	r.Get("/alfather/status", a.status)
	r.Delete("/alfather", a.unpair)
}

// MCPRoutes mounts POST /mcp outside /v1: it is authenticated by the consumer token, not a device token.
func (a *API) MCPRoutes(r chi.Router) {
	m := &MCP{Store: a.Store, Origin: a.BaseURL}
	r.Post("/mcp", m.ServeHTTP)
}

func (a *API) internal(w http.ResponseWriter, err error) {
	a.log().Error("alfather api", "err", err)
	httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
}

func (a *API) mcpURL(r *http.Request) string {
	base := strings.TrimRight(a.BaseURL, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/mcp"
}

// pair creates (or replaces) the Alfather consumer and returns its token once.
func (a *API) pair(w http.ResponseWriter, r *http.Request) {
	var in struct {
		// IncludeExisting lets Alfather read mail that is already stored; by default it starts at the newest.
		IncludeExisting bool `json:"include_existing"`
	}
	if r.ContentLength != 0 {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_body", err.Error())
			return
		}
	}
	raw, err := crypto.NewToken()
	if err != nil {
		a.internal(w, err)
		return
	}
	token := "alf_" + raw
	hash, err := hexBytes(crypto.HashToken(token))
	if err != nil {
		a.internal(w, err)
		return
	}
	id := ids.NewV7()
	if err := a.Store.ReplaceAlfather(r.Context(), id, hash, in.IncludeExisting); err != nil {
		a.internal(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{
		"consumer_id": id, "mcp_url": a.mcpURL(r), "token": token, "scope": Scope,
		"include_existing": in.IncludeExisting,
	})
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	c, err := a.Store.GetAlfather(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{
			"paired": false, "connected": false, "paired_at": nil, "last_seen_at": nil, "waiting": 0, "cursor_seq": 0, "mcp_url": nil,
		})
		return
	}
	if err != nil {
		a.internal(w, err)
		return
	}
	connected := c.LastSeenAt != nil && a.now().Sub(*c.LastSeenAt) <= ConnectedWithin
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"paired": true, "connected": connected, "paired_at": c.CreatedAt, "last_seen_at": c.LastSeenAt,
		"waiting": c.Waiting, "cursor_seq": c.CursorSeq, "mcp_url": a.mcpURL(r),
	})
}

func (a *API) unpair(w http.ResponseWriter, r *http.Request) {
	if _, err := a.Store.DeleteAlfather(r.Context()); err != nil {
		a.internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
