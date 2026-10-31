package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/store"
)

const (
	recentsKey   = "search_recents"
	maxRecents   = 10
	defaultLimit = 100 // also what clients got before pagination existed
	maxLimit     = 200
)

// API serves /search and /search/recents.
type API struct {
	Store *store.Store
}

// Routes mounts the endpoints on the protected /v1 router.
func (a *API) Routes(r chi.Router) {
	r.Get("/search", a.search)
	r.Get("/search/recents", a.listRecents)
	r.Post("/search/recents", a.addRecent)
	r.Delete("/search/recents", a.deleteRecents)
}

func internalErr(w http.ResponseWriter) {
	httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
}

// ---- search ----

type highlightJSON struct {
	Subject []Span `json:"subject"`
	From    []Span `json:"from"`
	Snippet []Span `json:"snippet"`
}

type resultJSON struct {
	ID              uuid.UUID     `json:"id"`
	BusinessID      *uuid.UUID    `json:"business_id"`
	FromAddr        *string       `json:"from_addr"`
	FromName        *string       `json:"from_name"`
	Subject         *string       `json:"subject"`
	Snippet         *string       `json:"snippet"`
	ReceivedAt      time.Time     `json:"received_at"`
	IsRead          bool          `json:"is_read"`
	Archived        bool          `json:"archived"`
	AttachmentCount int           `json:"attachment_count"`
	ThreadKey       string        `json:"thread_key"`
	Highlights      highlightJSON `json:"highlights"`
}

type groupJSON struct {
	Business *businessRef `json:"business"`
	Total    int          `json:"total"`
	Messages []resultJSON `json:"messages"`
}

type businessRef struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Domain string    `json:"domain"`
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (a *API) search(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("q")
	if utf8.RuneCountInString(raw) > MaxQueryLen {
		httpapi.WriteProblem(w, http.StatusBadRequest, "query_too_long", "q is limited to 200 characters")
		return
	}
	q := Parse(raw)
	if q.Empty() {
		httpapi.WriteProblem(w, http.StatusBadRequest, "empty_query", "q has no searchable terms")
		return
	}
	limit := defaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_limit", "limit must be 1..200")
			return
		}
		limit = n
	}
	p := store.SearchParams{WebTerms: q.WebTerms(), PrefixQuery: q.TSPrefixQuery(), From: q.From, To: q.To,
		HasAttachment: q.HasAttachment, Limit: limit + 1} // one extra row tells whether another page exists
	if v := r.URL.Query().Get("cursor"); v != "" {
		c, err := store.DecodeCursor(v)
		if err != nil {
			httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_cursor", "")
			return
		}
		p.Cursor = c
	}
	if v := r.URL.Query().Get("business"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_business", "business must be a uuid")
			return
		}
		p.BusinessID = &id
	}
	// Free text that tokenises to nothing (e.g. only punctuation) would match everything; it was
	// already dropped by Parse, but a query that is only an unusable prefix leaves nothing to run.
	if len(p.WebTerms) == 0 && p.PrefixQuery == "" && len(p.From) == 0 && len(p.To) == 0 && !p.HasAttachment {
		httpapi.WriteJSON(w, http.StatusOK, response(q, nil, ""))
		return
	}
	ctx := r.Context()
	ms, err := a.Store.SearchMessages(ctx, p)
	if err != nil {
		internalErr(w)
		return
	}
	next := ""
	if len(ms) > limit {
		ms = ms[:limit]
		next = store.EncodeCursor(store.Cursor{ReceivedAt: ms[limit-1].ReceivedAt, ID: ms[limit-1].ID})
	}
	refs, err := a.Store.ListBusinessRefs(ctx)
	if err != nil {
		internalErr(w)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, response(q, group(q, ms, refs), next))
}

func group(q Query, ms []store.Message, refs []store.BusinessRef) []groupJSON {
	var groups []groupJSON
	var none *groupJSON
	byBiz := map[uuid.UUID]*groupJSON{}
	for _, m := range ms {
		res := resultJSON{ID: m.ID, BusinessID: m.BusinessID, FromAddr: m.FromAddr, FromName: m.FromName, Subject: m.Subject,
			Snippet: m.Snippet, ReceivedAt: m.ReceivedAt, IsRead: m.IsRead, Archived: m.ArchivedAt != nil,
			AttachmentCount: m.AttachmentCount, ThreadKey: m.ThreadKey}
		res.Highlights = highlightJSON{Subject: nz(q.Highlights(deref(m.Subject))),
			From:    nz(q.Highlights(strings.TrimSpace(deref(m.FromName) + " " + deref(m.FromAddr)))),
			Snippet: nz(q.Highlights(deref(m.Snippet)))}
		if m.BusinessID == nil {
			if none == nil {
				none = &groupJSON{Messages: []resultJSON{}}
			}
			none.Total++
			none.Messages = append(none.Messages, res)
			continue
		}
		g := byBiz[*m.BusinessID]
		if g == nil {
			g = &groupJSON{Messages: []resultJSON{}}
			byBiz[*m.BusinessID] = g
		}
		g.Total++
		g.Messages = append(g.Messages, res)
	}
	// Groups follow the user's business order; unknown/unassigned mail goes last.
	for _, b := range refs {
		if g := byBiz[b.ID]; g != nil {
			g.Business = &businessRef{b.ID, b.Name, b.Domain}
			groups = append(groups, *g)
		}
	}
	if none != nil {
		groups = append(groups, *none)
	}
	return groups
}

func nz(s []Span) []Span {
	if s == nil {
		return []Span{}
	}
	return s
}

func response(q Query, groups []groupJSON, next string) map[string]any {
	if groups == nil {
		groups = []groupJSON{}
	}
	total := 0
	for _, g := range groups {
		total += g.Total
	}
	list := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	var nextCursor any
	if next != "" {
		nextCursor = next
	}
	return map[string]any{
		"groups":      groups,
		"total":       total, // results on this page
		"has_more":    next != "",
		"next_cursor": nextCursor,
		"parsed": map[string]any{
			"words": list(q.Words), "phrases": list(q.Phrases), "prefix": q.Prefix, "from": list(q.From), "to": list(q.To),
			"has_attachment": q.HasAttachment, "terms": list(q.Terms()),
		},
	}
}

// ---- recents ----

func (a *API) loadRecents(ctx context.Context, q *store.Queries) ([]string, error) {
	raw, err := q.GetSetting(ctx, recentsKey)
	if errors.Is(err, store.ErrNotFound) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return []string{}, nil
	}
	return out, nil
}

func writeRecents(w http.ResponseWriter, list []string) {
	if list == nil {
		list = []string{}
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"recents": list})
}

func (a *API) listRecents(w http.ResponseWriter, r *http.Request) {
	list, err := a.loadRecents(r.Context(), a.Store.Queries)
	if err != nil {
		internalErr(w)
		return
	}
	writeRecents(w, list)
}

func (a *API) addRecent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Q string `json:"q"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	in.Q = strings.TrimSpace(in.Q)
	if in.Q == "" || utf8.RuneCountInString(in.Q) > MaxQueryLen {
		httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_query", "q must be 1..200 characters")
		return
	}
	var out []string
	err := a.Store.WithTx(r.Context(), func(q *store.Queries) error {
		cur, err := a.loadRecents(r.Context(), q)
		if err != nil {
			return err
		}
		out = AddRecent(cur, in.Q)
		return q.SetSetting(r.Context(), recentsKey, out)
	})
	if err != nil {
		internalErr(w)
		return
	}
	writeRecents(w, out)
}

// AddRecent puts q first, dropping any case-insensitive duplicate, capped at maxRecents.
func AddRecent(cur []string, q string) []string {
	out := []string{q}
	for _, c := range cur {
		if !strings.EqualFold(c, q) && len(out) < maxRecents {
			out = append(out, c)
		}
	}
	return out
}

// deleteRecents clears all recents, or only the entry given by ?q=.
func (a *API) deleteRecents(w http.ResponseWriter, r *http.Request) {
	one := strings.TrimSpace(r.URL.Query().Get("q"))
	err := a.Store.WithTx(r.Context(), func(q *store.Queries) error {
		if one == "" {
			return q.SetSetting(r.Context(), recentsKey, []string{})
		}
		cur, err := a.loadRecents(r.Context(), q)
		if err != nil {
			return err
		}
		out := []string{}
		for _, c := range cur {
			if !strings.EqualFold(c, one) {
				out = append(out, c)
			}
		}
		return q.SetSetting(r.Context(), recentsKey, out)
	})
	if err != nil {
		internalErr(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
