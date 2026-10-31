package alfather_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/alfather"
	"github.com/casprine/inbox/server/internal/auth"
	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest"
	"github.com/casprine/inbox/server/internal/ingest/parse"
	"github.com/casprine/inbox/server/internal/rules"
	"github.com/casprine/inbox/server/internal/store"
	"github.com/casprine/inbox/server/internal/testutil"
)

type env struct {
	db  *testutil.DB
	ing *ingest.Worker
	bl  *blob.Store
	srv *httptest.Server
	biz store.Business
	n   int
}

func setup(t *testing.T) *env {
	t.Helper()
	ep := os.Getenv("TEST_S3_ENDPOINT")
	if ep == "" {
		t.Skip("TEST_S3_ENDPOINT not set")
	}
	db := testutil.NewDB(t)
	bl := blob.New(blob.Config{Endpoint: ep, Bucket: "inbox-mail", AccessKey: "minio", SecretKey: "minio12345"})
	e := &env{db: db, bl: bl, ing: &ingest.Worker{Store: db.Store, Blob: bl, Router: ingest.RulesRouter{}}}
	api := &alfather.API{Store: db.Store}
	asvc := &auth.Service{Store: db.Store, RateLimit: 1000, RateBurst: 1000}
	r := chi.NewRouter()
	api.MCPRoutes(r)
	asvc.Routes(r, func(r chi.Router) { api.Routes(r) })
	e.srv = httptest.NewServer(r)
	t.Cleanup(e.srv.Close)
	b, err := db.Store.CreateBusiness(context.Background(), store.Business{ID: ids.NewV7(), Name: "Acme", Domain: "acme.com", CFZoneID: "z", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	e.biz = b
	local := "invoices"
	lbl := "Acme/Invoices"
	if _, err := db.Store.CreateRuleTop(context.Background(), rules.Rule{ID: ids.NewV7(), Name: "Invoices", Enabled: true,
		BusinessID: &b.ID, MatchToLocal: &local, GmailLabel: &lbl, LabelOnly: true}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) api(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+"/v1"+path, rd)
	req.Header.Set("Authorization", "Bearer "+e.db.Token)
	if method != http.MethodGet {
		req.Header.Set("Idempotency-Key", uuid.NewString())
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return resp.StatusCode, m
}

func (e *env) pair(t *testing.T, body any) string {
	t.Helper()
	s, m := e.api(t, "POST", "/alfather/pairings", body)
	if s != 201 {
		t.Fatalf("pair: %d %v", s, m)
	}
	return m["token"].(string)
}

// rpc posts raw JSON to /mcp with the token; returns status and the decoded body (object or array).
func (e *env) rpc(t *testing.T, token string, body string, hdr ...string) (int, any) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var v any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("response not JSON: %q", b)
		}
	}
	return resp.StatusCode, v
}

var rpcID = 0

// call runs a tool and returns (structuredContent, isError, rpcError).
func (e *env) call(t *testing.T, token, tool string, args map[string]any) (map[string]any, bool, map[string]any) {
	t.Helper()
	rpcID++
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": rpcID, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	s, v := e.rpc(t, token, string(body))
	if s != 200 {
		t.Fatalf("tools/call %s: HTTP %d %v", tool, s, v)
	}
	m := v.(map[string]any)
	if er, ok := m["error"].(map[string]any); ok {
		return nil, false, er
	}
	res := m["result"].(map[string]any)
	isErr, _ := res["isError"].(bool)
	sc, _ := res["structuredContent"].(map[string]any)
	if !isErr {
		// The text content is the same JSON as the structured content.
		txt := res["content"].([]any)[0].(map[string]any)["text"].(string)
		var back map[string]any
		if err := json.Unmarshal([]byte(txt), &back); err != nil || fmt.Sprint(back) != fmt.Sprint(sc) {
			t.Fatalf("content text differs from structuredContent: %v vs %v", back, sc)
		}
	}
	return sc, isErr, nil
}

// mail ingests one message through the real path.
func (e *env) mail(t *testing.T, to, subject, body string, opts ...func(*ingest.RawOpts)) uuid.UUID {
	t.Helper()
	e.n++
	id := fmt.Sprintf("m%d@x", e.n)
	raw := []byte(fmt.Sprintf("From: Pat <pat@client.com>\r\nTo: %s\r\nMessage-ID: <%s>\r\nSubject: %s\r\nDate: Mon, 02 Mar 2026 10:00:00 +0000\r\nMIME-Version: 1.0\r\n"+
		"Content-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n--B\r\nContent-Type: application/pdf; name=\"inv.pdf\"\r\n"+
		"Content-Disposition: attachment; filename=\"inv.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n--B--\r\n",
		to, id, subject, body, base64.StdEncoding.EncodeToString([]byte("%PDF fake"))))
	key := "raw/2026/03/" + id + ".eml"
	if err := e.bl.Put(context.Background(), key, bytes.NewReader(raw), int64(len(raw)), nil); err != nil {
		t.Fatal(err)
	}
	p, err := parse.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	o := ingest.RawOpts{Key: key, EnvTo: to, ReceivedAt: time.Now().UTC(), Size: len(raw), Source: "edge"}
	for _, f := range opts {
		f(&o)
	}
	res, err := e.ing.Ingest(context.Background(), p, o)
	if err != nil || !res.Inserted {
		t.Fatalf("ingest: %+v %v", res, err)
	}
	return res.MessageID
}

func seqs(msgs []any) []float64 {
	var out []float64
	for _, m := range msgs {
		out = append(out, m.(map[string]any)["seq"].(float64))
	}
	return out
}

func TestPairingStatusAndUnpair(t *testing.T) {
	e := setup(t)
	s, m := e.api(t, "GET", "/alfather/status", nil)
	if s != 200 || m["paired"] != false || m["connected"] != false || m["waiting"] != float64(0) || m["mcp_url"] != nil {
		t.Fatalf("unpaired: %d %v", s, m)
	}
	s, m = e.api(t, "POST", "/alfather/pairings", nil)
	tok, _ := m["token"].(string)
	if s != 201 || !strings.HasPrefix(tok, "alf_") || len(tok) < 40 || m["scope"] != "mail.read" || !strings.HasSuffix(m["mcp_url"].(string), "/mcp") ||
		!strings.HasPrefix(m["mcp_url"].(string), "http://127.0.0.1:") || m["consumer_id"] == nil {
		t.Fatalf("pair: %d %v", s, m)
	}
	// Only a hash of the token is stored.
	var n int
	_ = e.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM consumers WHERE kind='alfather' AND encode(token_hash,'escape') LIKE '%alf_%'`).Scan(&n)
	if n != 0 {
		t.Fatal("token stored in the clear")
	}
	_, m = e.api(t, "GET", "/alfather/status", nil)
	if m["paired"] != true || m["connected"] != false || m["last_seen_at"] != nil || m["paired_at"] == nil {
		t.Fatalf("paired, never called: %v", m)
	}
	// Any authenticated MCP call counts as being connected.
	if s, _ := e.rpc(t, tok, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); s != 200 {
		t.Fatal(s)
	}
	_, m = e.api(t, "GET", "/alfather/status", nil)
	if m["connected"] != true || m["last_seen_at"] == nil {
		t.Fatalf("after ping: %v", m)
	}
	// Re-pairing replaces the old token.
	tok2 := e.pair(t, nil)
	if s, _ := e.rpc(t, tok, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); s != 401 {
		t.Fatalf("old token still works: %d", s)
	}
	if s, _ := e.rpc(t, tok2, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); s != 200 {
		t.Fatal("new token")
	}
	// Unpair.
	if s, _ := e.api(t, "DELETE", "/alfather", nil); s != 204 {
		t.Fatal(s)
	}
	if s, _ := e.api(t, "DELETE", "/alfather", nil); s != 204 {
		t.Fatal("unpair is idempotent")
	}
	if s, _ := e.rpc(t, tok2, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); s != 401 {
		t.Fatalf("token works after unpair: %d", s)
	}
	if _, m = e.api(t, "GET", "/alfather/status", nil); m["paired"] != false {
		t.Fatal(m)
	}
	// The app token is not an Alfather token, and vice versa.
	tok3 := e.pair(t, nil)
	if s, _ := e.rpc(t, e.db.Token, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); s != 401 {
		t.Fatalf("device token accepted by /mcp: %d", s)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/v1/alfather/status", nil)
	req.Header.Set("Authorization", "Bearer "+tok3)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 401 {
		t.Fatalf("alfather token accepted by /v1: %d", resp.StatusCode)
	}
	if s, m := e.api(t, "POST", "/alfather/pairings", map[string]any{"nope": 1}); s != 400 {
		t.Fatalf("%d %v", s, m)
	}
}

func TestProtocol(t *testing.T) {
	e := setup(t)
	tok := e.pair(t, nil)

	if s, _ := e.rpc(t, "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); s != 401 {
		t.Fatalf("no token: %d", s)
	}
	if s, _ := e.rpc(t, "wrong", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); s != 401 {
		t.Fatalf("wrong token: %d", s)
	}
	if s, _ := e.rpc(t, tok, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Origin", "https://evil.example"); s != 403 {
		t.Fatalf("browser origin: %d", s)
	}

	for _, tc := range []struct{ ask, want string }{{"2025-03-26", "2025-03-26"}, {"2025-06-18", "2025-06-18"}, {"1999-01-01", "2025-06-18"}} {
		s, v := e.rpc(t, tok, fmt.Sprintf(`{"jsonrpc":"2.0","id":7,"method":"initialize","params":{"protocolVersion":%q,"capabilities":{},"clientInfo":{"name":"alfather","version":"1"}}}`, tc.ask))
		res := v.(map[string]any)["result"].(map[string]any)
		if s != 200 || res["protocolVersion"] != tc.want || res["serverInfo"].(map[string]any)["name"] != "inbox" ||
			res["capabilities"].(map[string]any)["tools"] == nil || v.(map[string]any)["id"] != float64(7) {
			t.Fatalf("initialize(%s): %d %v", tc.ask, s, v)
		}
	}
	// Notifications get no body.
	if s, v := e.rpc(t, tok, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); s != 202 || v != nil {
		t.Fatalf("notification: %d %v", s, v)
	}
	// Tools.
	_, v := e.rpc(t, tok, `{"jsonrpc":"2.0","id":"a","method":"tools/list"}`)
	tools := v.(map[string]any)["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, x := range tools {
		x := x.(map[string]any)
		names = append(names, x["name"].(string))
		if x["description"] == "" || x["inputSchema"].(map[string]any)["type"] != "object" {
			t.Fatalf("tool %v", x)
		}
	}
	if strings.Join(names, ",") != "next_messages,ack,get_message,search" {
		t.Fatal(names)
	}
	if v.(map[string]any)["id"] != "a" {
		t.Fatal("string ids must round-trip")
	}
	// Errors.
	for body, code := range map[string]float64{
		`{"jsonrpc":"2.0","id":1,"method":"nope"}`:                                              -32601,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`:                            -32602,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"zzz","arguments":{}}}`: -32602,
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`:                                              -32600,
	} {
		s, v := e.rpc(t, tok, body)
		if s != 200 || v.(map[string]any)["error"].(map[string]any)["code"] != code {
			t.Errorf("%s: %d %v", body, s, v)
		}
	}
	if s, v := e.rpc(t, tok, `{not json`); s != 400 || v.(map[string]any)["error"].(map[string]any)["code"] != float64(-32700) {
		t.Fatalf("parse error: %d %v", s, v)
	}
	if s, _ := e.rpc(t, tok, ``); s != 400 {
		t.Fatalf("empty: %d", s)
	}
	// Batch: responses only for requests.
	s, v := e.rpc(t, tok, `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`)
	if arr, ok := v.([]any); !ok || s != 200 || len(arr) != 2 {
		t.Fatalf("batch: %d %v", s, v)
	}
	// A GET is not supported (no server-initiated stream).
	resp, err := http.Get(e.srv.URL + "/mcp")
	if err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp: %v %v", resp, err)
	}
}

func TestNextMessagesAckAndCursor(t *testing.T) {
	e := setup(t)
	// Mail that exists before pairing is not offered by default.
	e.mail(t, "hello@acme.com", "Before pairing", "old news")
	tok := e.pair(t, nil)
	out, _, _ := e.call(t, tok, "next_messages", nil)
	if len(out["messages"].([]any)) != 0 || out["waiting"] != float64(0) || out["has_more"] != false {
		t.Fatalf("fresh pairing: %v", out)
	}

	a := e.mail(t, "invoices@acme.com", "Invoice 17", "Please pay invoice 17 by Friday.")
	b := e.mail(t, "hello@acme.com", "Hello there", "Just saying hi.")
	c := e.mail(t, "hello@acme.com", "Third", "Third body")
	// Not offered: backfilled history, your own sent mail, failed items.
	e.mail(t, "hello@acme.com", "Old backfilled", "history", func(o *ingest.RawOpts) { o.Source = "backfill"; o.GmailID = "gb1" })
	e.mail(t, "hello@acme.com", "My reply", "sent", func(o *ingest.RawOpts) { o.Source = "gmail_thread"; o.Direction = "out"; o.GmailID = "go1" })
	_, _ = e.db.Pool.Exec(context.Background(), `INSERT INTO messages (id, source, state, failure_reason, message_id_hdr, thread_key, received_at)
VALUES ($1,'edge','failed','parse_failed','f1','f1',now())`, ids.NewV7())

	out, isErr, rerr := e.call(t, tok, "next_messages", nil)
	if isErr || rerr != nil {
		t.Fatal(isErr, rerr)
	}
	msgs := out["messages"].([]any)
	if len(msgs) != 3 || out["waiting"] != float64(3) || out["has_more"] != false {
		t.Fatalf("feed: %v", out)
	}
	sq := seqs(msgs)
	if !(sq[0] < sq[1] && sq[1] < sq[2]) {
		t.Fatalf("not in seq order: %v", sq)
	}
	m0 := msgs[0].(map[string]any)
	if m0["id"] != a.String() || m0["subject"] != "Invoice 17" || !strings.Contains(m0["text"].(string), "invoice 17 by Friday") ||
		m0["rule"] != "Invoices" || m0["route"] != "routed" || m0["to"] != "invoices@acme.com" || m0["text_truncated"] != false ||
		m0["from"].(map[string]any)["address"] != "pat@client.com" || m0["from"].(map[string]any)["name"] != "Pat" ||
		m0["business"].(map[string]any)["domain"] != "acme.com" || m0["is_read"] != false {
		t.Fatalf("message shape: %v", m0)
	}
	if att := m0["attachments"].([]any); len(att) != 1 || att[0].(map[string]any)["filename"] != "inv.pdf" || att[0].(map[string]any)["mime"] != "application/pdf" {
		t.Fatalf("attachments: %v", m0["attachments"])
	}
	if msgs[1].(map[string]any)["rule"] != nil || msgs[1].(map[string]any)["route"] != "unrouted" {
		t.Fatalf("unrouted message: %v", msgs[1])
	}
	// Reading does not consume: same answer again.
	again, _, _ := e.call(t, tok, "next_messages", nil)
	if fmt.Sprint(seqs(again["messages"].([]any))) != fmt.Sprint(sq) {
		t.Fatal("next_messages moved the cursor")
	}
	// Paging.
	page, _, _ := e.call(t, tok, "next_messages", map[string]any{"limit": 2})
	if len(page["messages"].([]any)) != 2 || page["has_more"] != true || page["waiting"] != float64(3) {
		t.Fatalf("page: %v", page)
	}
	_, st := e.api(t, "GET", "/alfather/status", nil)
	if st["waiting"] != float64(3) || st["cursor_seq"] == nil {
		t.Fatalf("status: %v", st)
	}

	// Ack the second message; only the third remains.
	ack, isErr, _ := e.call(t, tok, "ack", map[string]any{"up_to_seq": sq[1]})
	if isErr || ack["cursor_seq"] != sq[1] || ack["waiting"] != float64(1) {
		t.Fatalf("ack: %v %v", ack, isErr)
	}
	rest, _, _ := e.call(t, tok, "next_messages", nil)
	if r := rest["messages"].([]any); len(r) != 1 || r[0].(map[string]any)["id"] != c.String() {
		t.Fatalf("after ack: %v", rest)
	}
	// The cursor never goes back, and cannot jump past the newest message.
	back, _, _ := e.call(t, tok, "ack", map[string]any{"up_to_seq": 1})
	if back["cursor_seq"] != sq[1] {
		t.Fatalf("cursor regressed: %v", back)
	}
	if _, isErr, _ := e.call(t, tok, "ack", map[string]any{"up_to_seq": 99999}); !isErr {
		t.Fatal("ack beyond the newest message must be an error")
	}
	if _, _, rerr := e.call(t, tok, "ack", map[string]any{}); rerr == nil || rerr["code"] != float64(-32602) {
		t.Fatalf("missing up_to_seq: %v", rerr)
	}
	if _, _, rerr := e.call(t, tok, "ack", map[string]any{"up_to_seq": -1}); rerr == nil {
		t.Fatal("negative seq accepted")
	}
	if _, _, rerr := e.call(t, tok, "next_messages", map[string]any{"limit": 0}); rerr == nil {
		t.Fatal("limit 0 accepted")
	}
	if _, _, rerr := e.call(t, tok, "next_messages", map[string]any{"limit": 51}); rerr == nil {
		t.Fatal("limit 51 accepted")
	}
	if _, _, rerr := e.call(t, tok, "next_messages", map[string]any{"bogus": 1}); rerr == nil {
		t.Fatal("unknown argument accepted")
	}
	ack, _, _ = e.call(t, tok, "ack", map[string]any{"up_to_seq": sq[2]})
	if ack["waiting"] != float64(0) {
		t.Fatalf("%v", ack)
	}
	_ = b

	// Pairing with include_existing offers everything stored before.
	tok2 := e.pair(t, map[string]any{"include_existing": true})
	all, _, _ := e.call(t, tok2, "next_messages", map[string]any{"limit": 50})
	if n := len(all["messages"].([]any)); n != 4 { // Before pairing + the three; backfill/out/failed stay out
		t.Fatalf("include_existing returned %d messages", n)
	}
}

func TestGetMessageAndTruncation(t *testing.T) {
	e := setup(t)
	tok := e.pair(t, nil)
	first := e.mail(t, "invoices@acme.com", "Question", "Where is the invoice?")
	reply := e.mail(t, "invoices@acme.com", "Re: Question", "Thanks!")
	big := e.mail(t, "hello@acme.com", "Long one", strings.Repeat("word ", 6000)) // 30,000 chars

	got, isErr, _ := e.call(t, tok, "get_message", map[string]any{"id": first.String()})
	if isErr || got["id"] != first.String() || !strings.Contains(got["text"].(string), "Where is the invoice?") || got["message_id_header"] == nil ||
		got["auth_results"] == nil || got["thread"] == nil {
		t.Fatalf("get_message: %v %v", got, isErr)
	}
	_ = reply
	b, _, _ := e.call(t, tok, "get_message", map[string]any{"id": big.String()})
	if b["text_truncated"] != false || len(b["text"].(string)) < 29000 {
		t.Fatalf("get_message allows up to 100,000 chars: truncated=%v len=%d", b["text_truncated"], len(b["text"].(string)))
	}
	feed, _, _ := e.call(t, tok, "next_messages", map[string]any{"limit": 50})
	var seen bool
	for _, m := range feed["messages"].([]any) {
		m := m.(map[string]any)
		if m["id"] == big.String() {
			seen = true
			if m["text_truncated"] != true || len([]rune(m["text"].(string))) != 20000 {
				t.Fatalf("next_messages text must be cut at 20,000: truncated=%v len=%d", m["text_truncated"], len([]rune(m["text"].(string))))
			}
		}
	}
	if !seen {
		t.Fatal("big message missing from feed")
	}
	if _, isErr, _ := e.call(t, tok, "get_message", map[string]any{"id": uuid.NewString()}); !isErr {
		t.Fatal("unknown id must be a tool error")
	}
	if _, _, rerr := e.call(t, tok, "get_message", map[string]any{"id": "nope"}); rerr == nil || rerr["code"] != float64(-32602) {
		t.Fatalf("bad id: %v", rerr)
	}
}

func TestSearchTool(t *testing.T) {
	e := setup(t)
	tok := e.pair(t, nil)
	e.mail(t, "invoices@acme.com", "Invoice 17", "Please pay the rocket invoice")
	e.mail(t, "hello@acme.com", "Hello", "Nothing about money here")
	// Search sees history and archived mail that the feed never offers.
	e.mail(t, "hello@acme.com", "Old rocket order", "rocket parts", func(o *ingest.RawOpts) { o.Source = "backfill"; o.GmailID = "gb9" })

	res, isErr, rerr := e.call(t, tok, "search", map[string]any{"query": "rocket"})
	if isErr || rerr != nil {
		t.Fatal(isErr, rerr)
	}
	r := res["results"].([]any)
	if len(r) != 2 || r[0].(map[string]any)["subject"] != "Old rocket order" { // newest first
		t.Fatalf("results: %v", r)
	}
	if txt := r[0].(map[string]any)["text"].(string); len(txt) > 600 {
		t.Fatalf("search results carry only a short text excerpt, got %d chars", len(txt))
	}
	if res, _, _ := e.call(t, tok, "search", map[string]any{"query": "rocket", "limit": 1}); len(res["results"].([]any)) != 1 {
		t.Fatal("limit ignored")
	}
	if res, _, _ := e.call(t, tok, "search", map[string]any{"query": "has:attachment from:pat"}); len(res["results"].([]any)) != 3 {
		t.Fatalf("operators: %v", res)
	}
	if res, _, _ := e.call(t, tok, "search", map[string]any{"query": "rocket", "business": e.biz.ID.String()}); len(res["results"].([]any)) != 2 {
		t.Fatal("business filter")
	}
	if res, _, _ := e.call(t, tok, "search", map[string]any{"query": "rocket", "business": uuid.NewString()}); len(res["results"].([]any)) != 0 {
		t.Fatal("business filter must exclude")
	}
	if _, isErr, _ := e.call(t, tok, "search", map[string]any{"query": "  "}); !isErr {
		t.Fatal("empty query must be a tool error")
	}
	if _, _, rerr := e.call(t, tok, "search", map[string]any{"query": strings.Repeat("a", 201)}); rerr == nil {
		t.Fatal("overlong query accepted")
	}
	if _, _, rerr := e.call(t, tok, "search", map[string]any{"query": "x", "business": "zzz"}); rerr == nil {
		t.Fatal("bad business accepted")
	}
}
