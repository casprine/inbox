// Package rulesapi serves the /v1/rules endpoints.
package rulesapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/routing"
	"github.com/casprine/inbox/server/internal/rules"
	"github.com/casprine/inbox/server/internal/store"
)

// Rerouter queues the reroute_unrouted job.
type Rerouter interface {
	EnqueueReroute(ctx context.Context) error
}

const (
	maxBody      = 1 << 20
	defaultLimit = 200
	maxLimit     = 1000
)

// API holds handler dependencies.
type API struct {
	Store *store.Store
	// Reroute is nil when the job queue is not running; rule changes then skip the job.
	Reroute Rerouter
	Log     *slog.Logger
}

// Routes registers the endpoints on the protected /v1 group.
func (a *API) Routes(r chi.Router) {
	r.Get("/rules", a.list)
	r.Post("/rules", a.create)
	r.Put("/rules:order", a.order)
	r.Post("/rules:test", a.test)
	r.Patch("/rules/{id}", a.patch)
	r.Delete("/rules/{id}", a.delete)
	r.Post("/rules/{id}:apply", a.apply)
}

func (a *API) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}

func (a *API) internal(w http.ResponseWriter, err error) {
	a.log().Error("rules api", "err", err)
	httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
}

func (a *API) kick(ctx context.Context) {
	if a.Reroute == nil {
		return
	}
	if err := a.Reroute.EnqueueReroute(context.WithoutCancel(ctx)); err != nil {
		a.log().Error("enqueue reroute_unrouted", "err", err)
	}
}

func bad(w http.ResponseWriter, code, detail string) {
	httpapi.WriteProblem(w, http.StatusBadRequest, code, detail)
}

func notFound(w http.ResponseWriter) { httpapi.WriteProblem(w, http.StatusNotFound, "not_found", "") }

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		bad(w, "invalid_body", err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		notFound(w)
		return id, false
	}
	return id, true
}

// ---- JSON ----

type ruleJSON struct {
	ID                 uuid.UUID  `json:"id"`
	Name               string     `json:"name"`
	Position           int        `json:"position"`
	Enabled            bool       `json:"enabled"`
	BusinessID         *uuid.UUID `json:"business_id"`
	MatchToLocal       *string    `json:"match_to_local"`
	MatchFrom          *string    `json:"match_from"`
	MatchSubject       *string    `json:"match_subject"`
	GmailLabel         *string    `json:"gmail_label"`
	GmailArchive       bool       `json:"gmail_archive"`
	GmailMarkRead      bool       `json:"gmail_mark_read"`
	LabelOnly          bool       `json:"label_only"`
	ImportedFromFilter *string    `json:"imported_from_filter"`
	MessageCount       int        `json:"message_count"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func toJSON(r rules.Rule, n int) ruleJSON {
	return ruleJSON{ID: r.ID, Name: r.Name, Position: r.Position, Enabled: r.Enabled, BusinessID: r.BusinessID,
		MatchToLocal: r.MatchToLocal, MatchFrom: r.MatchFrom, MatchSubject: r.MatchSubject, GmailLabel: r.GmailLabel,
		GmailArchive: r.GmailArchive, GmailMarkRead: r.GmailMarkRead, LabelOnly: r.LabelOnly,
		ImportedFromFilter: r.ImportedFromFilter, MessageCount: n, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// ruleInput is the editable part of a rule (create body and draft in a test).
type ruleInput struct {
	ID            *uuid.UUID `json:"id"` // draft only: the rule being edited
	Name          string     `json:"name"`
	Enabled       *bool      `json:"enabled"`
	BusinessID    *uuid.UUID `json:"business_id"`
	MatchToLocal  *string    `json:"match_to_local"`
	MatchFrom     *string    `json:"match_from"`
	MatchSubject  *string    `json:"match_subject"`
	GmailLabel    *string    `json:"gmail_label"`
	GmailArchive  bool       `json:"gmail_archive"`
	GmailMarkRead bool       `json:"gmail_mark_read"`
	LabelOnly     *bool      `json:"label_only"`
}

func (in ruleInput) rule() rules.Rule {
	r := rules.Rule{Name: in.Name, Enabled: true, BusinessID: in.BusinessID, MatchToLocal: in.MatchToLocal,
		MatchFrom: in.MatchFrom, MatchSubject: in.MatchSubject, GmailLabel: in.GmailLabel,
		GmailArchive: in.GmailArchive, GmailMarkRead: in.GmailMarkRead, LabelOnly: true}
	if in.ID != nil {
		r.ID = *in.ID
	}
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
	if in.LabelOnly != nil {
		r.LabelOnly = *in.LabelOnly
	}
	return r
}

// normalize trims text fields and turns blanks into nil. The subject pattern is only
// blank-checked: spaces inside a regex are significant.
func normalize(r *rules.Rule) {
	r.Name = strings.TrimSpace(r.Name)
	trim := func(p **string) {
		if *p == nil {
			return
		}
		s := strings.TrimSpace(**p)
		if s == "" {
			*p = nil
			return
		}
		*p = &s
	}
	trim(&r.MatchToLocal)
	trim(&r.MatchFrom)
	trim(&r.GmailLabel)
	if r.MatchSubject != nil && strings.TrimSpace(*r.MatchSubject) == "" {
		r.MatchSubject = nil
	}
}

// problemFor writes the 422 for validation errors.
func problemFor(w http.ResponseWriter, errs []rules.FieldError) {
	code := "invalid_rule"
	for _, e := range errs {
		if e.IsPattern() {
			code = "rule_pattern_invalid"
			break
		}
	}
	first := errs[0]
	for _, e := range errs {
		if e.IsPattern() {
			first = e
			break
		}
	}
	ext := map[string]any{"field": first.Field, "errors": errs}
	if first.Column != nil {
		ext["column"] = *first.Column
	}
	httpapi.WriteProblemExt(w, http.StatusUnprocessableEntity, code, first.Message, ext)
}

// check normalizes and validates r, including that its business exists. It writes the
// problem and returns false on failure.
func (a *API) check(w http.ResponseWriter, r *http.Request, rule *rules.Rule) bool {
	normalize(rule)
	errs := rules.Validate(*rule)
	if rule.BusinessID != nil {
		if _, err := a.Store.GetBusiness(r.Context(), *rule.BusinessID); errors.Is(err, store.ErrNotFound) {
			errs = append(errs, rules.FieldError{Field: "business_id", Message: "That business does not exist."})
		} else if err != nil {
			a.internal(w, err)
			return false
		}
	}
	if len(errs) > 0 {
		problemFor(w, errs)
		return false
	}
	return true
}

// ---- handlers ----

func (a *API) listJSON(ctx context.Context) ([]ruleJSON, error) {
	rs, err := a.Store.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := a.Store.RuleMessageCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ruleJSON, 0, len(rs))
	for _, r := range rs {
		out = append(out, toJSON(r, counts[r.ID]))
	}
	return out, nil
}

func (a *API) writeList(w http.ResponseWriter, ctx context.Context) {
	out, err := a.listJSON(ctx)
	if err != nil {
		a.internal(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"rules": out})
}

func (a *API) list(w http.ResponseWriter, r *http.Request) { a.writeList(w, r.Context()) }

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var in ruleInput
	if !decode(w, r, &in) {
		return
	}
	in.ID = nil
	rule := in.rule()
	rule.ID = ids.NewV7()
	if !a.check(w, r, &rule) {
		return
	}
	created, err := a.Store.CreateRuleTop(r.Context(), rule)
	if err != nil {
		a.internal(w, err)
		return
	}
	a.kick(r.Context())
	httpapi.WriteJSON(w, http.StatusCreated, toJSON(created, 0))
}

func (a *API) patch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	if !decode(w, r, &raw) {
		return
	}
	cur, err := a.Store.GetRule(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		notFound(w)
		return
	} else if err != nil {
		a.internal(w, err)
		return
	}
	fields := map[string]any{
		"name": &cur.Name, "enabled": &cur.Enabled, "business_id": &cur.BusinessID,
		"match_to_local": &cur.MatchToLocal, "match_from": &cur.MatchFrom, "match_subject": &cur.MatchSubject,
		"gmail_label": &cur.GmailLabel, "gmail_archive": &cur.GmailArchive, "gmail_mark_read": &cur.GmailMarkRead,
		"label_only": &cur.LabelOnly,
	}
	for k, v := range raw {
		dst, known := fields[k]
		if !known {
			bad(w, "invalid_body", "unknown field "+k)
			return
		}
		if err := json.Unmarshal(v, dst); err != nil {
			bad(w, "invalid_body", k+": "+err.Error())
			return
		}
	}
	if !a.check(w, r, &cur) {
		return
	}
	updated, err := a.Store.UpdateRule(r.Context(), cur)
	if err != nil {
		a.internal(w, err)
		return
	}
	a.kick(r.Context())
	counts, err := a.Store.RuleMessageCounts(r.Context())
	if err != nil {
		a.internal(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, toJSON(updated, counts[id]))
}

func (a *API) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.Store.DeleteRule(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		notFound(w)
		return
	} else if err != nil {
		a.internal(w, err)
		return
	}
	a.kick(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) order(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []uuid.UUID `json:"ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := a.Store.SetRuleOrder(r.Context(), in.IDs); errors.Is(err, store.ErrNotFound) {
		bad(w, "invalid_ids", "ids must be distinct existing rules")
		return
	} else if err != nil {
		a.internal(w, err)
		return
	}
	a.kick(r.Context())
	a.writeList(w, r.Context())
}

type msgRef struct {
	ID         uuid.UUID  `json:"id"`
	BusinessID *uuid.UUID `json:"business_id"`
	ToLocal    *string    `json:"to_local"`
	FromAddr   *string    `json:"from_addr"`
	FromName   *string    `json:"from_name"`
	Subject    *string    `json:"subject"`
	ReceivedAt time.Time  `json:"received_at"`
}

func (a *API) test(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Rule  ruleInput `json:"rule"`
		Scope string    `json:"scope"`
		Limit int       `json:"limit"`
	}
	if !decode(w, r, &in) {
		return
	}
	unrouted, ok := parseScope(w, in.Scope, rules.ScopeAll)
	if !ok {
		return
	}
	if in.Limit < 0 || in.Limit > maxLimit {
		bad(w, "invalid_limit", "limit must be between 1 and 1000")
		return
	}
	if in.Limit == 0 {
		in.Limit = defaultLimit
	}
	draft := in.Rule.rule()
	if !a.check(w, r, &draft) {
		return
	}
	ctx := r.Context()
	existing, err := a.Store.ListRules(ctx)
	if err != nil {
		a.internal(w, err)
		return
	}
	if in.Rule.ID != nil {
		found := false
		for _, e := range existing {
			found = found || e.ID == *in.Rule.ID
		}
		if !found {
			notFound(w)
			return
		}
	}
	msgs, err := a.Store.RecentInbound(ctx, unrouted, in.Limit)
	if err != nil {
		a.internal(w, err)
		return
	}
	tm := make([]rules.TestMsg, 0, len(msgs))
	refs := make(map[uuid.UUID]msgRef, len(msgs))
	for _, m := range msgs {
		tm = append(tm, rules.TestMsg{ID: m.ID, Msg: routing.RulesMsg(m)})
		refs[m.ID] = msgRef{ID: m.ID, BusinessID: m.BusinessID, ToLocal: m.ToLocal, FromAddr: m.FromAddr,
			FromName: m.FromName, Subject: m.Subject, ReceivedAt: m.ReceivedAt}
	}
	res := rules.Test(draft, existing, tm)
	type caught struct {
		Message msgRef `json:"message"`
		ByRule  struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"name"`
		} `json:"by_rule"`
	}
	would := make([]msgRef, 0, len(res.WouldMatch))
	for _, id := range res.WouldMatch {
		would = append(would, refs[id])
	}
	early := make([]caught, 0, len(res.CaughtEarlier))
	for _, c := range res.CaughtEarlier {
		e := caught{Message: refs[c.MessageID]}
		e.ByRule.ID, e.ByRule.Name = c.By.ID, c.By.Name
		early = append(early, e)
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"scope": scopeName(unrouted), "scanned": len(msgs), "would_match": would, "caught_earlier": early,
	})
}

func scopeName(unrouted bool) string {
	if unrouted {
		return rules.ScopeUnrouted
	}
	return rules.ScopeAll
}

// parseScope maps a scope string to "unrouted only"; def is used when it is empty.
func parseScope(w http.ResponseWriter, s, def string) (unrouted, ok bool) {
	if s == "" {
		s = def
	}
	switch s {
	case rules.ScopeAll:
		return false, true
	case rules.ScopeUnrouted:
		return true, true
	}
	bad(w, "invalid_request", "scope must be all or unrouted")
	return false, false
}

func (a *API) apply(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Scope string `json:"scope"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		bad(w, "invalid_body", err.Error())
		return
	}
	unrouted, ok := parseScope(w, in.Scope, rules.ScopeUnrouted)
	if !ok {
		return
	}
	rule, err := a.Store.GetRule(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		notFound(w)
		return
	} else if err != nil {
		a.internal(w, err)
		return
	}
	if !rule.Enabled {
		httpapi.WriteProblem(w, http.StatusConflict, "rule_disabled", "enable the rule before applying it")
		return
	}
	n, err := routing.Reroute(r.Context(), a.Store, unrouted)
	if err != nil {
		a.internal(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"scope": scopeName(unrouted), "routed": n})
}
