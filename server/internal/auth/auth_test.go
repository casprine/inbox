package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/casprine/inbox/server/internal/auth"
	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/migrate"
	"github.com/casprine/inbox/server/internal/store"
)

const testPassword = "correct horse battery staple"

func newStore(t *testing.T) *store.Store {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
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
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return store.New(pool)
}

type harness struct {
	svc     *auth.Service
	srv     *httptest.Server
	calls   atomic.Int32
	started chan struct{} // /slow signals here when it is running
	release chan struct{} // /slow waits for this
	fails   atomic.Int32  // /flaky answers 500 while this is positive
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pw, err := auth.NewPasswordVerifier(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{svc: &auth.Service{
		Store: newStore(t), Password: pw, RateLimit: 1000, RateBurst: 1000,
		// roomy limits: tests sign in many times; the dedicated lockout tests use their own limiter
		Login: &auth.LoginLimiter{FreeFailures: 1000, GlobalPerSecond: 1000, GlobalBurst: 1000},
	}}
	h.started, h.release = make(chan struct{}, 10), make(chan struct{})
	r := chi.NewRouter()
	h.svc.Routes(r, func(r chi.Router) {
		r.Post("/slow", func(w http.ResponseWriter, _ *http.Request) {
			n := h.calls.Add(1)
			h.started <- struct{}{}
			<-h.release
			httpapi.WriteJSON(w, http.StatusCreated, map[string]int32{"n": n})
		})
		r.Post("/flaky", func(w http.ResponseWriter, _ *http.Request) {
			n := h.calls.Add(1)
			if h.fails.Add(-1) >= 0 {
				httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
				return
			}
			httpapi.WriteJSON(w, http.StatusCreated, map[string]int32{"n": n})
		})
		r.Post("/boom", func(http.ResponseWriter, *http.Request) {
			h.calls.Add(1)
			panic(http.ErrAbortHandler)
		})
		r.Post("/things", func(w http.ResponseWriter, _ *http.Request) {
			n := h.calls.Add(1)
			httpapi.WriteJSON(w, http.StatusCreated, map[string]int32{"n": n})
		})
		r.Get("/ping", func(w http.ResponseWriter, req *http.Request) {
			d, _ := auth.DeviceFrom(req.Context())
			httpapi.WriteJSON(w, 200, map[string]string{"device": d.ID.String()})
		})
	})
	h.srv = httptest.NewServer(r)
	t.Cleanup(h.srv.Close)
	return h
}

func (h *harness) do(t *testing.T, method, path, token string, body any, hdr map[string]string) (int, []byte, http.Header) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes(), resp.Header
}

func (h *harness) login(t *testing.T, password string) (int, string, string) {
	t.Helper()
	st, body, _ := h.do(t, "POST", "/v1/auth/login", "", map[string]string{"password": password, "device_name": "iPhone"}, nil)
	var out struct {
		DeviceID string `json:"device_id"`
		Token    string `json:"token"`
	}
	json.Unmarshal(body, &out)
	return st, out.DeviceID, out.Token
}

func problemCode(b []byte) string {
	var p httpapi.Problem
	json.Unmarshal(b, &p)
	return p.Code
}

func TestLoginAndBearer(t *testing.T) {
	h := newHarness(t)
	st, body, _ := h.do(t, "POST", "/v1/auth/login", "", map[string]string{"password": "wrong password!!"}, nil)
	if st != 401 || problemCode(body) != "invalid_credentials" {
		t.Fatalf("wrong password: %d %s", st, body)
	}
	st, id, tok := h.login(t, testPassword)
	if st != 200 || id == "" || tok == "" {
		t.Fatalf("login: %d", st)
	}
	if st, _, _ := h.do(t, "GET", "/v1/ping", "", nil, nil); st != 401 {
		t.Fatalf("no token: %d", st)
	}
	if st, _, _ := h.do(t, "GET", "/v1/ping", "wrong", nil, nil); st != 401 {
		t.Fatalf("bad token: %d", st)
	}
	st, body, _ = h.do(t, "GET", "/v1/ping", tok, nil, nil)
	if st != 200 || !strings.Contains(string(body), id) {
		t.Fatalf("ping: %d %s", st, body)
	}
	// revoke
	if st, _, _ := h.do(t, "DELETE", "/v1/devices/"+id, tok, nil, nil); st != 204 {
		t.Fatalf("revoke: %d", st)
	}
	if st, _, _ := h.do(t, "GET", "/v1/ping", tok, nil, nil); st != 401 {
		t.Fatalf("revoked token still works: %d", st)
	}
}

// setup_step lets the app resume first-run setup exactly where it stopped (C-19).
func TestLoginSetupStep(t *testing.T) {
	h := newHarness(t)
	step := func() string {
		t.Helper()
		st, body, _ := h.do(t, "POST", "/v1/auth/login", "", map[string]string{"password": testPassword, "device_name": "iPhone"}, nil)
		var out struct {
			Token     string `json:"token"`
			SetupStep string `json:"setup_step"`
		}
		if st != 200 || json.Unmarshal(body, &out) != nil || out.Token == "" {
			t.Fatalf("login: %d %s", st, body)
		}
		return out.SetupStep
	}
	ctx := context.Background()
	if got := step(); got != "domains" {
		t.Fatalf("empty server: %q, want domains", got)
	}
	if _, err := h.svc.Store.CreateBusiness(ctx, store.Business{ID: uuid.New(), Name: "Acme", Domain: "acme.test", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if got := step(); got != "gmail" {
		t.Fatalf("business but no Gmail: %q, want gmail", got)
	}
	if _, err := h.svc.Store.UpsertGmailAccount(ctx, uuid.New(), "me@gmail.test", []byte("sealed"), "k1"); err != nil {
		t.Fatal(err)
	}
	if got := step(); got != "done" {
		t.Fatalf("Gmail connected: %q, want done", got)
	}
}

func TestLoginRejectsBadRequests(t *testing.T) {
	h := newHarness(t)
	for _, body := range []any{map[string]string{}, map[string]string{"password": ""}, "not an object"} {
		if st, _, _ := h.do(t, "POST", "/v1/auth/login", "", body, nil); st != 400 {
			t.Errorf("body %v: %d, want 400", body, st)
		}
	}
	// the removed Apple route is gone: it now falls into the token-protected /v1 group
	if st, _, _ := h.do(t, "POST", "/v1/auth/apple", "", map[string]string{"identity_token": "x"}, nil); st != 401 {
		t.Errorf("/v1/auth/apple answered %d, want 401 (no such public route)", st)
	}
}

// The password and the device token must not be stored in clear anywhere in the database, and the
// responses to failed logins must not contain either.
func TestNothingLeaks(t *testing.T) {
	h := newHarness(t)
	st, body, _ := h.do(t, "POST", "/v1/auth/login", "", map[string]string{"password": "not-the-password-0"}, nil)
	if st != 401 || strings.Contains(string(body), testPassword) || strings.Contains(string(body), "not-the-password-0") {
		t.Fatalf("failed login: %d %s", st, body)
	}
	_, _, tok := h.login(t, testPassword)
	var hit int
	err := h.svc.Store.Pool().QueryRow(context.Background(), `
SELECT (SELECT count(*) FROM settings WHERE value::text ILIKE '%' || $1 || '%')
     + (SELECT count(*) FROM devices WHERE name ILIKE '%' || $1 || '%' OR encode(token_hash,'hex') = $2)
     + (SELECT count(*) FROM idempotency_keys WHERE response::text ILIKE '%' || $1 || '%')`, testPassword, tok).Scan(&hit)
	if err != nil || hit != 0 {
		t.Fatalf("password or raw token found in the database: %d %v", hit, err)
	}
	var cols int
	if err := h.svc.Store.Pool().QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns WHERE table_name='devices' AND column_name='apple_sub'`).Scan(&cols); err != nil || cols != 0 {
		t.Fatalf("devices.apple_sub still exists (%d) %v", cols, err)
	}
	var owner int
	if err := h.svc.Store.Pool().QueryRow(context.Background(), `SELECT count(*) FROM settings WHERE key IN ('owner_apple_sub','bootstrap_code_hash')`).Scan(&owner); err != nil || owner != 0 {
		t.Fatalf("legacy owner settings exist: %d %v", owner, err)
	}
}

func TestLoginLockoutOverHTTP(t *testing.T) {
	h := newHarness(t)
	h.svc.Login = &auth.LoginLimiter{FreeFailures: 2, BaseDelay: time.Minute, GlobalPerSecond: 1000, GlobalBurst: 1000}
	for i := 0; i < 3; i++ { // three failures: the third starts the lock
		if st, _, _ := h.do(t, "POST", "/v1/auth/login", "", map[string]string{"password": "wrong password!!"}, nil); st != 401 {
			t.Fatalf("attempt %d: %d", i, st)
		}
	}
	// Even the right password is refused while locked, with Retry-After.
	st, body, hd := h.do(t, "POST", "/v1/auth/login", "", map[string]string{"password": testPassword}, nil)
	if st != 429 || problemCode(body) != "rate_limited" {
		t.Fatalf("locked: %d %s", st, body)
	}
	if n, err := strconv.Atoi(hd.Get("Retry-After")); err != nil || n < 1 || n > 60 {
		t.Fatalf("Retry-After = %q", hd.Get("Retry-After"))
	}
	if strings.Contains(string(body), testPassword) {
		t.Fatal("429 body leaks the password")
	}
}

func TestStartupRefusesWeakPassword(t *testing.T) {
	for _, pw := range []string{"", "short", "elevenchars"} {
		if _, err := auth.NewPasswordVerifier(pw); err == nil {
			t.Errorf("password %q accepted", pw)
		}
	}
	if _, err := auth.NewPasswordVerifier("twelve chars"); err != nil {
		t.Errorf("12 characters rejected: %v", err)
	}
}

func TestIdempotency(t *testing.T) {
	h := newHarness(t)
	_, _, tok := h.login(t, testPassword)
	_, _, tok2 := h.login(t, testPassword)
	k := map[string]string{"Idempotency-Key": "abc"}
	st1, b1, _ := h.do(t, "POST", "/v1/things", tok, nil, k)
	st2, b2, hd := h.do(t, "POST", "/v1/things", tok, nil, k)
	if st1 != 201 || st2 != 201 || string(b1) != string(b2) || hd.Get("Idempotency-Replayed") != "true" || h.calls.Load() != 1 {
		t.Fatalf("replay: %d %d %s %s calls=%d", st1, st2, b1, b2, h.calls.Load())
	}
	// same key, other device → executes again
	h.do(t, "POST", "/v1/things", tok2, nil, k)
	if h.calls.Load() != 2 {
		t.Fatalf("other device calls=%d", h.calls.Load())
	}
	// no key → executes every time
	h.do(t, "POST", "/v1/things", tok, nil, nil)
	h.do(t, "POST", "/v1/things", tok, nil, nil)
	if h.calls.Load() != 4 {
		t.Fatalf("no key calls=%d", h.calls.Load())
	}
	// expired key is re-executed
	_, err := h.svc.Store.Pool().Exec(context.Background(), `UPDATE idempotency_keys SET at = now() - interval '25 hours'`)
	if err != nil {
		t.Fatal(err)
	}
	h.do(t, "POST", "/v1/things", tok, nil, k)
	if h.calls.Load() != 5 {
		t.Fatalf("expired calls=%d", h.calls.Load())
	}
}

func TestRateLimit(t *testing.T) {
	h := newHarness(t)
	h.svc.RateLimit, h.svc.RateBurst = 0.001, 3
	_, _, tok := h.login(t, testPassword)
	var got429 bool
	for range 6 {
		st, body, _ := h.do(t, "GET", "/v1/ping", tok, nil, nil)
		if st == 429 {
			got429 = problemCode(body) == "rate_limited"
		}
	}
	if !got429 {
		t.Fatal("expected 429 with rate_limited code")
	}
}

func TestIdempotencyClaimBlocksSimultaneousDuplicates(t *testing.T) {
	h := newHarness(t)
	_, _, tok := h.login(t, testPassword)
	k := map[string]string{"Idempotency-Key": "same"}

	first := make(chan int, 1)
	go func() {
		st, _, _ := h.do(t, "POST", "/v1/slow", tok, nil, k)
		first <- st
	}()
	<-h.started // the first request is inside the handler

	// While it runs, every duplicate is refused without running the handler.
	for i := 0; i < 3; i++ {
		st, body, hd := h.do(t, "POST", "/v1/slow", tok, nil, k)
		if st != 409 || problemCode(body) != "request_in_progress" || hd.Get("Retry-After") == "" {
			t.Fatalf("duplicate %d: %d %s", i, st, body)
		}
	}
	// A different key is independent.
	go func() { h.do(t, "POST", "/v1/slow", tok, nil, map[string]string{"Idempotency-Key": "other"}) }()
	<-h.started
	h.release <- struct{}{}
	h.release <- struct{}{}
	if st := <-first; st != 201 {
		t.Fatalf("first request: %d", st)
	}
	// After it finished the key replays its response; the handler ran once per distinct key.
	st, _, hd := h.do(t, "POST", "/v1/slow", tok, nil, k)
	if st != 201 || hd.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay: %d %v", st, hd)
	}
	if n := h.calls.Load(); n != 2 {
		t.Fatalf("handler ran %d times, want 2 (one per key)", n)
	}
}

func TestIdempotencyManyRacersRunOnce(t *testing.T) {
	h := newHarness(t)
	_, _, tok := h.login(t, testPassword)
	k := map[string]string{"Idempotency-Key": "race"}
	const n = 12
	codes := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() {
			st, _, _ := h.do(t, "POST", "/v1/slow", tok, nil, k)
			codes <- st
		}()
	}
	<-h.started
	// Give the losers time to hit the claim, then let the winner finish.
	time.Sleep(300 * time.Millisecond)
	h.release <- struct{}{}
	counts := map[int]int{}
	for i := 0; i < n; i++ {
		counts[<-codes]++
	}
	if h.calls.Load() != 1 || counts[201]+counts[409] != n || counts[201] < 1 {
		t.Fatalf("handler ran %d times; responses %v", h.calls.Load(), counts)
	}
}

func TestIdempotencyFailedRequestCanBeRetried(t *testing.T) {
	h := newHarness(t)
	_, _, tok := h.login(t, testPassword)
	k := map[string]string{"Idempotency-Key": "retry"}
	h.fails.Store(1)
	if st, _, _ := h.do(t, "POST", "/v1/flaky", tok, nil, k); st != 500 {
		t.Fatalf("first: %d", st)
	}
	// The 500 released the claim: the same key runs again immediately and succeeds, then replays.
	if st, _, _ := h.do(t, "POST", "/v1/flaky", tok, nil, k); st != 201 {
		t.Fatalf("retry: %d", st)
	}
	if st, _, hd := h.do(t, "POST", "/v1/flaky", tok, nil, k); st != 201 || hd.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay: %d", st)
	}
	if h.calls.Load() != 2 {
		t.Fatalf("calls=%d", h.calls.Load())
	}
}

func TestIdempotencyPanicReleasesClaim(t *testing.T) {
	h := newHarness(t)
	_, _, tok := h.login(t, testPassword)
	k := map[string]string{"Idempotency-Key": "boom"}
	req, _ := http.NewRequest("POST", h.srv.URL+"/v1/boom", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Idempotency-Key", "boom")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close() // the aborted handler normally closes the connection; either way is fine
	}
	var n int
	if err := h.svc.Store.Pool().QueryRow(context.Background(), `SELECT count(*) FROM idempotency_keys WHERE key='boom'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("claim left behind after a panic: %d %v", n, err)
	}
	_ = k
}

func TestIdempotencyAbandonedClaimIsTakenOver(t *testing.T) {
	h := newHarness(t)
	_, _, tok := h.login(t, testPassword)
	k := map[string]string{"Idempotency-Key": "stale"}
	// A claim whose owner vanished (crash) blocks for a while...
	var dev string
	if err := h.svc.Store.Pool().QueryRow(context.Background(), `SELECT id::text FROM devices LIMIT 1`).Scan(&dev); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Store.Pool().Exec(context.Background(), `INSERT INTO idempotency_keys (key, device_id, response, at) VALUES ('stale', $1, NULL, now())`, dev); err != nil {
		t.Fatal(err)
	}
	if st, body, _ := h.do(t, "POST", "/v1/things", tok, nil, k); st != 409 || problemCode(body) != "request_in_progress" {
		t.Fatalf("fresh claim: %d %s", st, body)
	}
	// ...then is taken over once it is clearly abandoned.
	if _, err := h.svc.Store.Pool().Exec(context.Background(), `UPDATE idempotency_keys SET at = now() - interval '3 minutes' WHERE key='stale'`); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := h.do(t, "POST", "/v1/things", tok, nil, k); st != 201 {
		t.Fatalf("takeover: %d", st)
	}
}
