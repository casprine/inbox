package rulesapi_test

import (
	"bytes"
	"context"
	"encoding/hex"
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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/casprine/inbox/server/internal/auth"
	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/crypto"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest"
	"github.com/casprine/inbox/server/internal/migrate"
	"github.com/casprine/inbox/server/internal/rulesapi"
	"github.com/casprine/inbox/server/internal/store"
)

type env struct {
	st    *store.Store
	pool  *pgxpool.Pool
	blob  *blob.Store
	srv   *httptest.Server
	token string
}

func setup(t *testing.T) *env {
	t.Helper()
	base, ep := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_S3_ENDPOINT")
	if base == "" || ep == "" {
		t.Skip("TEST_DATABASE_URL / TEST_S3_ENDPOINT not set")
	}
	ctx := context.Background()
	schema := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	url := fmt.Sprintf("%s%ssearch_path=%s,public", base, sep, schema)
	if err := migrate.Up(ctx, url); err != nil {
		t.Fatal(err)
	}
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)
	bl := blob.New(blob.Config{Endpoint: ep, Bucket: "inbox-mail", AccessKey: "minio", SecretKey: "minio12345"})
	token := "test-token-" + uuid.NewString()
	hash, _ := hex.DecodeString(crypto.HashToken(token))
	if err := st.CreateDevice(ctx, ids.NewV7(), "test", hash); err != nil {
		t.Fatal(err)
	}
	rc, err := ingest.NewRiver(ingest.Deps{Pool: pool, Store: st, Blob: bl})
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	svc := &auth.Service{Store: st, RateLimit: 1000, RateBurst: 1000}
	api := &rulesapi.API{Store: st, Reroute: ingest.RiverEnqueuer{Client: rc}}
	r := chi.NewRouter()
	svc.Routes(r, func(r chi.Router) { api.Routes(r) })
	srv := httptest.NewServer(r)
	t.Cleanup(func() {
		srv.Close()
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = rc.Stop(sctx)
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return &env{st: st, pool: pool, blob: bl, srv: srv, token: token}
}

func (e *env) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+"/v1"+path, rd)
	req.Header.Set("Authorization", "Bearer "+e.token)
	if method != http.MethodGet {
		req.Header.Set("Idempotency-Key", uuid.NewString())
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("decode %s: %v", b, err)
		}
	}
	return resp.StatusCode, out
}

func (e *env) mustDo(t *testing.T, want int, method, path string, body any) map[string]any {
	t.Helper()
	code, out := e.do(t, method, path, body)
	if code != want {
		t.Fatalf("%s %s = %d, want %d: %v", method, path, code, want, out)
	}
	return out
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (e *env) msg(t *testing.T, biz *uuid.UUID, local, from, subject, route string, age time.Duration) store.Message {
	t.Helper()
	p := func(s string) *string { return &s }
	m := store.Message{ID: ids.NewV7(), BusinessID: biz, Source: "edge", State: "stored", MessageIDHdr: "<" + uuid.NewString() + ">",
		ToLocal: p(local), FromAddr: p(from), Subject: p(subject), ThreadKey: uuid.NewString(), Route: route,
		ReceivedAt: time.Now().UTC().Add(-age)}
	if ok, err := e.st.InsertMessage(context.Background(), m); err != nil || !ok {
		t.Fatalf("insert %v %v", ok, err)
	}
	return m
}

func (e *env) route(t *testing.T, id uuid.UUID) (route string, rule *uuid.UUID) {
	t.Helper()
	m, err := e.st.GetMessage(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return m.Route, m.RuleID
}

func list(out map[string]any) []map[string]any {
	var rs []map[string]any
	for _, r := range out["rules"].([]any) {
		rs = append(rs, r.(map[string]any))
	}
	return rs
}

func TestRulesAPI(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	biz, err := e.st.CreateBusiness(ctx, store.Business{ID: ids.NewV7(), Name: "My", Domain: "mydomain.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// validation: 422 with field and column
	code, prob := e.do(t, "POST", "/rules", map[string]any{"name": "bad", "match_subject": "abc(def"})
	if code != 422 || prob["code"] != "rule_pattern_invalid" || prob["field"] != "match_subject" || prob["column"] == nil || prob["errors"] == nil {
		t.Fatalf("invalid pattern: %d %v", code, prob)
	}
	if code, prob = e.do(t, "POST", "/rules", map[string]any{"name": ""}); code != 422 || prob["code"] != "invalid_rule" {
		t.Fatalf("empty name: %d %v", code, prob)
	}
	if code, prob = e.do(t, "POST", "/rules", map[string]any{"name": "x", "business_id": uuid.NewString()}); code != 422 || prob["field"] != "business_id" {
		t.Fatalf("bad business: %d %v", code, prob)
	}
	if code, _ = e.do(t, "POST", "/rules", map[string]any{"name": "x", "bogus": 1}); code != 400 {
		t.Fatalf("unknown field: %d", code)
	}

	// create: new rules go on top; label_only defaults to true
	shop := e.mustDo(t, 201, "POST", "/rules", map[string]any{"name": "Shop", "business_id": biz.ID, "match_to_local": "shop",
		"gmail_label": "Shop", "gmail_archive": true, "gmail_mark_read": true, "label_only": false})
	alice := e.mustDo(t, 201, "POST", "/rules", map[string]any{"name": "Alice", "match_from": "@example.com", "gmail_label": "People", "gmail_archive": true})
	if alice["label_only"] != true || alice["position"].(float64) != 0 {
		t.Fatalf("alice %v", alice)
	}
	rs := list(e.mustDo(t, 200, "GET", "/rules", nil))
	if len(rs) != 2 || rs[0]["name"] != "Alice" || rs[1]["name"] != "Shop" || rs[1]["position"].(float64) != 1 {
		t.Fatalf("list %v", rs)
	}
	aliceID, shopID := alice["id"].(string), shop["id"].(string)

	// ingest through the real router: Alice is first, label-only so no archive want
	key := fmt.Sprintf("raw/test-%s.eml", uuid.NewString())
	raw, _ := os.ReadFile("../ingest/parse/testdata/plain.eml")
	if err := e.blob.Put(ctx, key, bytes.NewReader(raw), int64(len(raw)), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.blob.Delete(ctx, key) })
	w := &ingest.Worker{Store: e.st, Blob: e.blob, Router: ingest.RulesRouter{}}
	t0 := time.Now() // the write-back is due 30 s after routing; measure against this, not against "now" (slow machines)
	if err := w.Process(ctx, ingest.IngestArgs{Key: key, EnvFrom: "alice@example.com", EnvTo: "shop+orders@mydomain.com"}); err != nil {
		t.Fatal(err)
	}
	m, err := e.st.GetMessageByRawKey(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if m.Route != "routed" || m.RuleID == nil || m.RuleID.String() != aliceID || m.BusinessID == nil || *m.BusinessID != biz.ID {
		t.Fatalf("ingest routing %+v", m)
	}
	var label *string
	var arch, read bool
	var state string
	var due time.Time
	row := e.pool.QueryRow(ctx, `SELECT want_label, want_archived, want_read, state::text, next_attempt_at FROM gmail_sync WHERE message_id=$1`, m.ID)
	if err := row.Scan(&label, &arch, &read, &state, &due); err != nil {
		t.Fatal(err)
	}
	if label == nil || *label != "People" || arch || read || state != "pending" || due.Before(t0.Add(25*time.Second)) || due.After(t0.Add(60*time.Second)) {
		t.Fatalf("gmail_sync %v %v %v %v %v", label, arch, read, state, due)
	}

	// reorder: Shop first. Unrouted mail is picked up by the job; routed mail stays put.
	un1 := e.msg(t, &biz.ID, "shop", "bob@other.com", "Order 1", "unrouted", time.Hour)
	un2 := e.msg(t, &biz.ID, "shop", "alice@example.com", "Order 2", "unrouted", 2*time.Hour)
	un3 := e.msg(t, &biz.ID, "info", "bob@other.com", "Hi", "unrouted", 3*time.Hour)
	e.mustDo(t, 400, "PUT", "/rules:order", map[string]any{"ids": []string{uuid.NewString()}})
	rs = list(e.mustDo(t, 200, "PUT", "/rules:order", map[string]any{"ids": []string{shopID, aliceID}}))
	if rs[0]["name"] != "Shop" || rs[1]["name"] != "Alice" {
		t.Fatalf("order %v", rs)
	}
	waitFor(t, "reroute", func() bool { r, _ := e.route(t, un2.ID); return r == "routed" })
	if r, id := e.route(t, un1.ID); r != "routed" || id == nil || id.String() != shopID {
		t.Fatalf("un1 %s %v", r, id)
	}
	if r, id := e.route(t, un2.ID); r != "routed" || id.String() != shopID {
		t.Fatalf("un2 should go to Shop (first), got %s %v", r, id)
	}
	if r, id := e.route(t, un3.ID); r != "unrouted" || id != nil {
		t.Fatalf("un3 %s %v", r, id)
	}
	if r, id := e.route(t, m.ID); r != "routed" || id.String() != aliceID {
		t.Fatalf("routed message must not move: %s %v", r, id)
	}
	// Shop is not label-only: archive + read wanted
	row = e.pool.QueryRow(ctx, `SELECT want_label, want_archived, want_read FROM gmail_sync WHERE message_id=$1`, un1.ID)
	if err := row.Scan(&label, &arch, &read); err != nil || label == nil || *label != "Shop" || !arch || !read {
		t.Fatalf("shop wants %v %v %v %v", label, arch, read, err)
	}
	if n := countRows(t, e, `SELECT count(*) FROM gmail_sync WHERE message_id=$1`, un3.ID); n != 0 {
		t.Fatalf("unrouted message has gmail_sync")
	}

	// tester: new draft on top catches un3 and others; editing Alice (second) is caught earlier by Shop
	res := e.mustDo(t, 200, "POST", "/rules:test", map[string]any{"scope": "all", "rule": map[string]any{"name": "Bob", "match_from": "@other.com"}})
	if res["scanned"].(float64) != 4 || len(res["would_match"].([]any)) != 2 || len(res["caught_earlier"].([]any)) != 0 {
		t.Fatalf("test new: %v", res)
	}
	res = e.mustDo(t, 200, "POST", "/rules:test", map[string]any{"rule": map[string]any{"id": aliceID, "name": "Alice", "match_from": "@example.com"}})
	ce := res["caught_earlier"].([]any)
	if len(ce) != 2 || len(res["would_match"].([]any)) != 0 {
		t.Fatalf("test edit: %v", res)
	}
	for _, c := range ce {
		if c.(map[string]any)["by_rule"].(map[string]any)["name"] != "Shop" {
			t.Fatalf("test edit: %v", res)
		}
	}
	res = e.mustDo(t, 200, "POST", "/rules:test", map[string]any{"scope": "unrouted", "limit": 1, "rule": map[string]any{"name": "Any", "match_to_local": "*"}})
	if res["scanned"].(float64) != 1 || len(res["would_match"].([]any)) != 1 {
		t.Fatalf("test unrouted: %v", res)
	}
	if code, _ = e.do(t, "POST", "/rules:test", map[string]any{"scope": "nope", "rule": map[string]any{"name": "x"}}); code != 400 {
		t.Fatalf("scope: %d", code)
	}
	if code, _ = e.do(t, "POST", "/rules:test", map[string]any{"rule": map[string]any{"id": uuid.NewString(), "name": "x"}}); code != 404 {
		t.Fatalf("unknown draft id: %d", code)
	}
	code, prob = e.do(t, "POST", "/rules:test", map[string]any{"rule": map[string]any{"name": "x", "match_subject": "[a"}})
	if code != 422 || prob["code"] != "rule_pattern_invalid" {
		t.Fatalf("test invalid: %d %v", code, prob)
	}

	// patch: clear a field, null handling, validation
	p := e.mustDo(t, 200, "PATCH", "/rules/"+aliceID, map[string]any{"match_from": nil, "match_to_local": "info", "label_only": false})
	if p["match_from"] != nil || p["match_to_local"] != "info" || p["label_only"] != false || p["message_count"].(float64) != 1 {
		t.Fatalf("patch %v", p)
	}
	waitFor(t, "reroute after patch", func() bool { r, _ := e.route(t, un3.ID); return r == "routed" })
	if code, prob = e.do(t, "PATCH", "/rules/"+aliceID, map[string]any{"match_subject": "(x"}); code != 422 || prob["column"] == nil {
		t.Fatalf("patch invalid %d %v", code, prob)
	}
	if code, _ = e.do(t, "PATCH", "/rules/"+uuid.NewString(), map[string]any{"name": "x"}); code != 404 {
		t.Fatalf("patch 404: %d", code)
	}
	if code, _ = e.do(t, "PATCH", "/rules/"+aliceID, map[string]any{"nope": 1}); code != 400 {
		t.Fatalf("patch unknown: %d", code)
	}

	// apply: disable Shop, scope=all re-evaluates routed messages; disabled rule can't be applied
	e.mustDo(t, 200, "PATCH", "/rules/"+shopID, map[string]any{"enabled": false})
	if code, prob = e.do(t, "POST", "/rules/"+shopID+":apply", nil); code != 409 || prob["code"] != "rule_disabled" {
		t.Fatalf("apply disabled: %d %v", code, prob)
	}
	e.mustDo(t, 200, "PATCH", "/rules/"+shopID, map[string]any{"enabled": true, "match_to_local": "nothing"})
	out := e.mustDo(t, 200, "POST", "/rules/"+aliceID+":apply", map[string]any{"scope": "all"})
	if out["scope"] != "all" {
		t.Fatalf("apply %v", out)
	}
	for _, id := range []uuid.UUID{un1.ID, un2.ID} {
		if r, rid := e.route(t, id); r != "unrouted" && !(r == "routed" && rid != nil && rid.String() == aliceID) {
			t.Fatalf("after apply all: %s %v", r, rid)
		}
	}
	if r, rid := e.route(t, un3.ID); r != "routed" || rid.String() != aliceID {
		t.Fatalf("un3 after apply %s %v", r, rid)
	}
	if code, _ = e.do(t, "POST", "/rules/"+aliceID+":apply", map[string]any{"scope": "x"}); code != 400 {
		t.Fatalf("apply scope: %d", code)
	}

	// delete: messages go back to unrouted, positions are compacted
	e.mustDo(t, 204, "DELETE", "/rules/"+aliceID, nil)
	if code, _ = e.do(t, "DELETE", "/rules/"+aliceID, nil); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}
	waitFor(t, "unrouted after delete", func() bool { r, _ := e.route(t, un3.ID); return r == "unrouted" })
	rs = list(e.mustDo(t, 200, "GET", "/rules", nil))
	if len(rs) != 1 || rs[0]["position"].(float64) != 0 {
		t.Fatalf("after delete %v", rs)
	}
}

func countRows(t *testing.T, e *env, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
