// Package alfather pairs Alfather (the Mac assistant) with the server and serves it the mail
// feed over MCP (streamable HTTP, JSON responses). Alfather connects out to the server; the
// server never dials Alfather (DECISIONS D-006).
package alfather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/crypto"
	"github.com/casprine/inbox/server/internal/mail/search"
	"github.com/casprine/inbox/server/internal/store"
)

// JSON-RPC 2.0 error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

// Protocol versions the server speaks, newest first.
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const (
	maxBody        = 1 << 20
	defaultLimit   = 20
	maxLimit       = 50
	maxTextRunes   = 20000  // per message in next_messages and search
	maxFullRunes   = 100000 // get_message
	serverName     = "inbox"
	serverVersion  = "1"
	instructionsTx = "Inbox mail feed. Call next_messages to read new mail in order, then ack with the highest seq you handled; " +
		"unacknowledged mail is returned again. get_message and search read any stored message."
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func errResp(id json.RawMessage, code int, msg string) rpcResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{code, msg}}
}

// MCP serves the /mcp endpoint.
type MCP struct {
	Store *store.Store
	// Origin is the allowed Origin of browser callers (empty: any request that carries an Origin is refused).
	Origin string
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="inbox"`)
	writeJSON(w, http.StatusUnauthorized, errResp(nil, codeInvalidRequest, "unauthorized"))
}

// originOK protects against DNS-rebinding: native clients send no Origin.
func (m *MCP) originOK(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	if m.Origin == "" {
		return false
	}
	u1, e1 := url.Parse(o)
	u2, e2 := url.Parse(m.Origin)
	return e1 == nil && e2 == nil && strings.EqualFold(u1.Scheme+"://"+u1.Host, u2.Scheme+"://"+u2.Host)
}

// Authenticate resolves a bearer token to the paired consumer (and marks it seen).
func (m *MCP) authenticate(r *http.Request) (store.Consumer, bool) {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || tok == "" {
		return store.Consumer{}, false
	}
	hash, err := hexBytes(crypto.HashToken(tok))
	if err != nil {
		return store.Consumer{}, false
	}
	c, err := m.Store.AlfatherByTokenHash(r.Context(), hash)
	return c, err == nil
}

// ServeHTTP handles POST /mcp: one JSON-RPC message or a batch.
func (m *MCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !m.originOK(r) {
		writeJSON(w, http.StatusForbidden, errResp(nil, codeInvalidRequest, "origin not allowed"))
		return
	}
	c, ok := m.authenticate(r)
	if !ok {
		unauthorized(w)
		return
	}
	body, err := readAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errResp(nil, codeParse, "request too large or unreadable"))
		return
	}
	body = trimSpace(body)
	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, errResp(nil, codeInvalidRequest, "empty body"))
		return
	}
	var reqs []json.RawMessage
	batch := body[0] == '['
	if batch {
		if err := json.Unmarshal(body, &reqs); err != nil || len(reqs) == 0 {
			writeJSON(w, http.StatusBadRequest, errResp(nil, codeParse, "parse error"))
			return
		}
	} else {
		reqs = []json.RawMessage{body}
	}
	var out []rpcResponse
	for _, raw := range reqs {
		var req rpcRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			if !batch {
				writeJSON(w, http.StatusBadRequest, errResp(nil, codeParse, "parse error"))
				return
			}
			out = append(out, errResp(nil, codeParse, "parse error"))
			continue
		}
		if req.Method == "" { // a response from the client; nothing to do
			continue
		}
		if req.JSONRPC != "2.0" {
			if len(req.ID) > 0 {
				out = append(out, errResp(req.ID, codeInvalidRequest, `jsonrpc must be "2.0"`))
			}
			continue
		}
		if len(req.ID) == 0 { // notification
			continue
		}
		out = append(out, m.dispatch(r.Context(), c, req))
	}
	switch {
	case len(out) == 0:
		w.WriteHeader(http.StatusAccepted)
	case batch:
		writeJSON(w, http.StatusOK, out)
	default:
		writeJSON(w, http.StatusOK, out[0])
	}
}

func (m *MCP) dispatch(ctx context.Context, c store.Consumer, req rpcRequest) rpcResponse {
	ok := func(res any) rpcResponse { return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: res} }
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := protocolVersions[0]
		for _, s := range protocolVersions {
			if s == p.ProtocolVersion {
				v = s
			}
		}
		return ok(map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
			"instructions":    instructionsTx,
		})
	case "ping":
		return ok(map[string]any{})
	case "tools/list":
		return ok(map[string]any{"tools": toolList()})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return errResp(req.ID, codeInvalidParams, "tools/call needs a name")
		}
		res, perr := m.callTool(ctx, c, p.Name, p.Arguments)
		if perr != nil {
			return errResp(req.ID, perr.Code, perr.Message)
		}
		return ok(res)
	}
	return errResp(req.ID, codeMethodNotFound, "method not found: "+req.Method)
}

func toolResult(structured any) map[string]any {
	b, _ := json.Marshal(structured)
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(b)}},
		"structuredContent": structured,
		"isError":           false,
	}
}

func toolError(msg string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": true}
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func toolList() []map[string]any {
	return []map[string]any{
		{
			"name":        "next_messages",
			"description": "Mail that arrived after your cursor, oldest first, with sender, business, rule and plain text. Does not move the cursor: call ack when you have handled them.",
			"inputSchema": obj(map[string]any{"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": maxLimit, "default": defaultLimit}}),
			"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		{
			"name":        "ack",
			"description": "Mark mail up to and including this seq as handled. The cursor only moves forward.",
			"inputSchema": obj(map[string]any{"up_to_seq": map[string]any{"type": "integer", "minimum": 0}}, "up_to_seq"),
			"annotations": map[string]any{"readOnlyHint": false, "idempotentHint": true, "destructiveHint": false, "openWorldHint": false},
		},
		{
			"name":        "get_message",
			"description": "One message in full (plain text up to 100,000 characters, headers, attachments list, its thread).",
			"inputSchema": obj(map[string]any{"id": map[string]any{"type": "string", "format": "uuid"}}, "id"),
			"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		{
			"name": "search",
			"description": "Search stored mail. Words match subject, sender, body and attachment names; operators: from:, to:, has:attachment, \"quoted phrases\". " +
				"Newest first, archived mail included.",
			"inputSchema": obj(map[string]any{
				"query":    map[string]any{"type": "string", "maxLength": search.MaxQueryLen},
				"business": map[string]any{"type": "string", "format": "uuid", "description": "limit to one business"},
				"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": maxLimit, "default": defaultLimit},
			}, "query"),
			"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
	}
}

func (m *MCP) callTool(ctx context.Context, c store.Consumer, name string, args json.RawMessage) (map[string]any, *rpcError) {
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	invalid := func(msg string) (map[string]any, *rpcError) { return nil, &rpcError{codeInvalidParams, msg} }
	internal := func(err error) (map[string]any, *rpcError) {
		return nil, &rpcError{codeInternal, "internal error"}
	}
	dec := func(v any) error {
		d := json.NewDecoder(strings.NewReader(string(args)))
		d.DisallowUnknownFields()
		return d.Decode(v)
	}
	switch name {
	case "next_messages":
		var a struct {
			Limit *int `json:"limit"`
		}
		if err := dec(&a); err != nil {
			return invalid("bad arguments: " + err.Error())
		}
		limit := defaultLimit
		if a.Limit != nil {
			if *a.Limit < 1 || *a.Limit > maxLimit {
				return invalid(fmt.Sprintf("limit must be 1 to %d", maxLimit))
			}
			limit = *a.Limit
		}
		// Read the cursor afresh: another call may have acked since authentication.
		cur, err := m.cursor(ctx, c.ID)
		if err != nil {
			return internal(err)
		}
		feed, more, err := m.Store.FeedAfter(ctx, cur, limit)
		if err != nil {
			return internal(err)
		}
		msgs, err := m.render(ctx, feed, maxTextRunes)
		if err != nil {
			return internal(err)
		}
		waiting, err := m.Store.CountAfter(ctx, cur)
		if err != nil {
			return internal(err)
		}
		return toolResult(map[string]any{"messages": msgs, "cursor_seq": cur, "has_more": more, "waiting": waiting}), nil

	case "ack":
		var a struct {
			UpToSeq *int64 `json:"up_to_seq"`
		}
		if err := dec(&a); err != nil || a.UpToSeq == nil {
			return invalid("ack needs up_to_seq")
		}
		if *a.UpToSeq < 0 {
			return invalid("up_to_seq cannot be negative")
		}
		max, err := m.Store.MaxSeq(ctx)
		if err != nil {
			return internal(err)
		}
		if *a.UpToSeq > max {
			return toolError(fmt.Sprintf("up_to_seq %d is beyond the newest message (%d)", *a.UpToSeq, max)), nil
		}
		cur, err := m.Store.AdvanceCursor(ctx, c.ID, *a.UpToSeq)
		if err != nil {
			return internal(err)
		}
		waiting, err := m.Store.CountAfter(ctx, cur)
		if err != nil {
			return internal(err)
		}
		return toolResult(map[string]any{"cursor_seq": cur, "waiting": waiting}), nil

	case "get_message":
		var a struct {
			ID string `json:"id"`
		}
		if err := dec(&a); err != nil {
			return invalid("bad arguments: " + err.Error())
		}
		id, err := uuid.Parse(a.ID)
		if err != nil {
			return invalid("id must be a message id")
		}
		f, err := m.Store.FeedByID(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return toolError("no such message"), nil
		}
		if err != nil {
			return internal(err)
		}
		msgs, err := m.render(ctx, []store.FeedMessage{f}, maxFullRunes)
		if err != nil {
			return internal(err)
		}
		thread, err := m.Store.ListThread(ctx, f.ThreadKey)
		if err != nil {
			return internal(err)
		}
		tl := make([]map[string]any, 0, len(thread))
		for _, t := range thread {
			tl = append(tl, map[string]any{"id": t.ID, "direction": t.Direction, "from": addr(t.FromName, t.FromAddr),
				"subject": t.Subject, "received_at": t.ReceivedAt})
		}
		msgs[0]["thread"] = tl
		msgs[0]["message_id_header"] = f.MessageIDHdr
		msgs[0]["auth_results"] = json.RawMessage(orNull(f.AuthResults))
		return toolResult(msgs[0]), nil

	case "search":
		var a struct {
			Query    string  `json:"query"`
			Business *string `json:"business"`
			Limit    *int    `json:"limit"`
		}
		if err := dec(&a); err != nil {
			return invalid("bad arguments: " + err.Error())
		}
		if utf8.RuneCountInString(a.Query) > search.MaxQueryLen {
			return invalid(fmt.Sprintf("query is limited to %d characters", search.MaxQueryLen))
		}
		q := search.Parse(a.Query)
		if q.Empty() {
			return toolError("the query has no searchable terms"), nil
		}
		limit := defaultLimit
		if a.Limit != nil {
			if *a.Limit < 1 || *a.Limit > maxLimit {
				return invalid(fmt.Sprintf("limit must be 1 to %d", maxLimit))
			}
			limit = *a.Limit
		}
		p := store.SearchParams{WebTerms: q.WebTerms(), PrefixQuery: q.TSPrefixQuery(), From: q.From, To: q.To,
			HasAttachment: q.HasAttachment, Limit: limit}
		if a.Business != nil {
			id, err := uuid.Parse(*a.Business)
			if err != nil {
				return invalid("business must be a uuid")
			}
			p.BusinessID = &id
		}
		if len(p.WebTerms) == 0 && p.PrefixQuery == "" && len(p.From) == 0 && len(p.To) == 0 && !p.HasAttachment {
			return toolResult(map[string]any{"results": []any{}}), nil
		}
		ms, err := m.Store.SearchMessages(ctx, p)
		if err != nil {
			return internal(err)
		}
		ids := make([]uuid.UUID, len(ms))
		for i, x := range ms {
			ids[i] = x.ID
		}
		feed, err := m.Store.FeedByIDs(ctx, ids)
		if err != nil {
			return internal(err)
		}
		byID := map[uuid.UUID]store.FeedMessage{}
		for _, f := range feed {
			byID[f.ID] = f
		}
		ordered := make([]store.FeedMessage, 0, len(ms))
		for _, x := range ms { // keep the search order (newest first)
			if f, ok := byID[x.ID]; ok {
				ordered = append(ordered, f)
			}
		}
		res, err := m.render(ctx, ordered, 500)
		if err != nil {
			return internal(err)
		}
		return toolResult(map[string]any{"results": res}), nil
	}
	return nil, &rpcError{codeInvalidParams, "unknown tool: " + name}
}

func (m *MCP) cursor(ctx context.Context, id uuid.UUID) (int64, error) {
	// AdvanceCursor with 0 is a no-op read of the current value (GREATEST never lowers it).
	return m.Store.AdvanceCursor(ctx, id, 0)
}

func addr(name, address *string) map[string]any {
	return map[string]any{"name": name, "address": address}
}

func orNull(b []byte) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

// render turns feed rows into the tool JSON, cutting text at maxRunes.
func (m *MCP) render(ctx context.Context, feed []store.FeedMessage, maxRunes int) ([]map[string]any, error) {
	ids := make([]uuid.UUID, len(feed))
	for i, f := range feed {
		ids[i] = f.ID
	}
	atts, err := m.Store.AttachmentsFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(feed))
	for _, f := range feed {
		text, truncated := "", false
		if f.Body != nil {
			text = *f.Body
			if utf8.RuneCountInString(text) > maxRunes {
				text, truncated = string([]rune(text)[:maxRunes]), true
			}
		}
		al := []map[string]any{}
		for _, a := range atts[f.ID] {
			al = append(al, map[string]any{"filename": a.Filename, "mime": a.Mime, "size_bytes": a.SizeBytes})
		}
		var biz any
		if f.BusinessID != nil && f.BusinessName != nil {
			biz = map[string]any{"id": f.BusinessID, "name": f.BusinessName, "domain": f.BusinessDomain}
		}
		out = append(out, map[string]any{
			"id": f.ID, "seq": f.Seq, "received_at": f.ReceivedAt, "direction": f.Direction, "business": biz,
			"route": f.Route, "rule": f.RuleName, "from": addr(f.FromName, f.FromAddr), "to": f.EnvTo, "to_local": f.ToLocal,
			"subject": f.Subject, "text": text, "text_truncated": truncated, "attachments": al,
			"is_read": f.IsRead, "archived": f.ArchivedAt != nil, "thread_key": f.ThreadKey,
		})
	}
	return out, nil
}
