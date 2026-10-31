package gmail

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/casprine/inbox/server/internal/auth"
	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/crypto"
	"github.com/casprine/inbox/server/internal/events"
	"github.com/casprine/inbox/server/internal/gmail/gmailtest"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest"
	"github.com/casprine/inbox/server/internal/mail"
	"github.com/casprine/inbox/server/internal/migrate"
	"github.com/casprine/inbox/server/internal/store"
)

type recorder struct {
	mu sync.Mutex
	ev []events.Event
}

func (r *recorder) Publish(e events.Event) { r.mu.Lock(); r.ev = append(r.ev, e); r.mu.Unlock() }
func (r *recorder) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.ev {
		out = append(out, e.Type)
	}
	return out
}

type env struct {
	st    *store.Store
	pool  *pgxpool.Pool
	svc   *Service
	fake  *gmailtest.Fake
	srv   *httptest.Server
	token string
	ev    *recorder
	eng   *Engine
	blob  *blob.Store // nil unless TEST_S3_ENDPOINT is set
	ing   *ingest.Worker
}

func setup(t *testing.T) *env {
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
	dbURL := fmt.Sprintf("%s%ssearch_path=%s,public", base, sep, schema)
	if err := migrate.Up(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	pool, err := store.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)
	token := "test-token-" + uuid.NewString()
	hash, _ := hex.DecodeString(crypto.HashToken(token))
	if err := st.CreateDevice(ctx, ids.NewV7(), "test", hash); err != nil {
		t.Fatal(err)
	}
	fake := gmailtest.New()
	keys, err := crypto.KeyringFromBase64("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	ev := &recorder{}
	svc := &Service{Store: st, Keys: keys, Events: ev, Cfg: Config{
		ClientID: "cid", ClientSecret: "secret", RedirectURL: "http://app.test/oauth/google/callback",
		AuthURL: fake.AuthURL(), TokenURL: fake.TokenURL(), RevokeURL: fake.RevokeURL(), APIBase: fake.APIBase(),
	}}
	var bl *blob.Store
	var ing *ingest.Worker
	if ep := os.Getenv("TEST_S3_ENDPOINT"); ep != "" {
		bl = blob.New(blob.Config{Endpoint: ep, Bucket: "inbox-mail", AccessKey: "minio", SecretKey: "minio12345"})
		ing = &ingest.Worker{Store: st, Blob: bl, Router: ingest.RulesRouter{}}
	}
	eng := &Engine{Svc: svc, Store: st, Jitter: func(d time.Duration) time.Duration { return d }}
	a := &API{Svc: svc, Engine: eng}
	mapi := &mail.API{Store: st}
	asvc := &auth.Service{Store: st, RateLimit: 1000, RateBurst: 1000}
	r := chi.NewRouter()
	a.PublicRoutes(r)
	asvc.Routes(r, func(r chi.Router) { a.Routes(r); mapi.Routes(r) })
	srv := httptest.NewServer(r)
	t.Cleanup(func() {
		srv.Close()
		fake.Close()
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return &env{st: st, pool: pool, svc: svc, fake: fake, srv: srv, token: token, ev: ev, eng: eng, blob: bl, ing: ing}
}

func (e *env) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	b := e.raw(t, method, path, body)
	var m map[string]any
	if len(b.body) > 0 {
		_ = json.Unmarshal(b.body, &m)
	}
	return b.status, m
}

type rawResp struct {
	status int
	body   []byte
	header http.Header
}

func (e *env) raw(t *testing.T, method, path string, body any) rawResp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
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
	return rawResp{resp.StatusCode, b, resp.Header}
}

// connect runs the whole OAuth handshake against the fake and returns the callback redirect.
func (e *env) connect(t *testing.T, code string) string {
	t.Helper()
	status, m := e.do(t, "POST", "/gmail/connect", nil)
	if status != 200 {
		t.Fatalf("connect status %d %v", status, m)
	}
	u, err := url.Parse(m["auth_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	e.fake.IssueCode(code, q.Get("code_challenge"))
	return e.callback(t, url.Values{"code": {code}, "state": {q.Get("state")}})
}

func (e *env) callback(t *testing.T, q url.Values) string {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(e.srv.URL + "/oauth/google/callback?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback status %d", resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

// insertMessage adds a minimal stored message and returns its id.
func (e *env) insertMessage(t *testing.T, msgID string) uuid.UUID {
	t.Helper()
	id := ids.NewV7()
	_, err := e.pool.Exec(context.Background(), `INSERT INTO messages (id, source, message_id_hdr, thread_key, received_at)
VALUES ($1,'edge',$2,$3,now())`, id, msgID, "t-"+msgID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestConnectCallbackStatusDisconnect(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	status, m := e.do(t, "GET", "/gmail/status", nil)
	if status != 200 || m["state"] != "not_connected" || m["email"] != nil {
		t.Fatalf("initial status: %d %v", status, m)
	}

	status, m = e.do(t, "POST", "/gmail/connect", nil)
	if status != 200 {
		t.Fatalf("connect: %d %v", status, m)
	}
	u, _ := url.Parse(m["auth_url"].(string))
	q := u.Query()
	if u.Host != strings.TrimPrefix(e.fake.URL, "http://") || q.Get("code_challenge_method") != "S256" ||
		q.Get("access_type") != "offline" || q.Get("client_id") != "cid" || q.Get("scope") != ScopeModify+" "+ScopeSettings ||
		q.Get("redirect_uri") != "http://app.test/oauth/google/callback" || q.Get("state") == "" {
		t.Fatalf("auth url wrong: %s", u)
	}
	e.fake.IssueCode("good", q.Get("code_challenge"))
	loc := e.callback(t, url.Values{"code": {"good"}, "state": {q.Get("state")}})
	if loc != "inbox://gmail/connected?status=ok" {
		t.Fatalf("redirect %q", loc)
	}

	status, m = e.do(t, "GET", "/gmail/status", nil)
	if status != 200 || m["state"] != "connected" || m["email"] != "sam@gmail.com" || m["waiting"] != float64(0) {
		t.Fatalf("status after connect: %d %v", status, m)
	}
	if got := e.ev.types(); len(got) != 1 || got[0] != events.TypeGmailStatus {
		t.Fatalf("events: %v", got)
	}

	// The refresh token is encrypted at rest and decrypts with the keyring.
	acct, err := e.st.GetGmailAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(acct.RefreshTokenEnc), "refresh-1") || acct.KeyID != "k1" {
		t.Fatalf("token not sealed: key=%s", acct.KeyID)
	}
	if pt, err := e.svc.Keys.Open(acct.RefreshTokenEnc, []byte("gmail_refresh_token")); err != nil || string(pt) != "refresh-1" {
		t.Fatalf("open: %q %v", pt, err)
	}

	// The same state cannot be replayed.
	if loc := e.callback(t, url.Values{"code": {"good"}, "state": {q.Get("state")}}); loc != "inbox://gmail/connected?reason=invalid_state&status=error" {
		t.Fatalf("replay redirect %q", loc)
	}

	// Disconnect revokes at Google, forgets the account, and a second call is a conflict.
	if s, _ := e.do(t, "POST", "/gmail:disconnect", nil); s != 204 {
		t.Fatalf("disconnect status %d", s)
	}
	if len(e.fake.Revoked) != 1 || e.fake.Revoked[0] != "refresh-1" {
		t.Fatalf("revoked: %v", e.fake.Revoked)
	}
	if _, m = e.do(t, "GET", "/gmail/status", nil); m["state"] != "not_connected" {
		t.Fatalf("status after disconnect: %v", m)
	}
	if s, m := e.do(t, "POST", "/gmail:disconnect", nil); s != 409 || m["code"] != "gmail_not_connected" {
		t.Fatalf("second disconnect: %d %v", s, m)
	}
}

func TestCallbackRejections(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for name, tc := range map[string]struct {
		q    func(state string) url.Values
		want string
	}{
		"unknown state": {func(string) url.Values { return url.Values{"code": {"x"}, "state": {"nope"}} }, "invalid_state"},
		"no state":      {func(string) url.Values { return url.Values{"code": {"x"}} }, "invalid_state"},
		"declined":      {func(s string) url.Values { return url.Values{"error": {"access_denied"}, "state": {s}} }, "access_denied"},
		"bad code":      {func(s string) url.Values { return url.Values{"code": {"never-issued"}, "state": {s}} }, "exchange_failed"},
		"no code":       {func(s string) url.Values { return url.Values{"state": {s}} }, "invalid_request"},
	} {
		t.Run(name, func(t *testing.T) {
			_, m := e.do(t, "POST", "/gmail/connect", nil)
			u, _ := url.Parse(m["auth_url"].(string))
			loc := e.callback(t, tc.q(u.Query().Get("state")))
			want := "inbox://gmail/connected?reason=" + tc.want + "&status=error"
			if loc != want {
				t.Fatalf("got %q want %q", loc, want)
			}
			if _, err := e.st.GetGmailAccount(ctx); err == nil {
				t.Fatal("account must not be stored")
			}
		})
	}

	t.Run("expired state", func(t *testing.T) {
		if err := e.st.CreateGmailOAuthState(ctx, "old", nil, "v", -time.Minute); err != nil {
			t.Fatal(err)
		}
		if loc := e.callback(t, url.Values{"code": {"x"}, "state": {"old"}}); !strings.Contains(loc, "invalid_state") {
			t.Fatalf("expired state accepted: %q", loc)
		}
	})

	t.Run("wrong PKCE verifier", func(t *testing.T) {
		_, m := e.do(t, "POST", "/gmail/connect", nil)
		u, _ := url.Parse(m["auth_url"].(string))
		e.fake.IssueCode("c-pkce", "different-challenge")
		loc := e.callback(t, url.Values{"code": {"c-pkce"}, "state": {u.Query().Get("state")}})
		if !strings.Contains(loc, "exchange_failed") {
			t.Fatalf("PKCE mismatch accepted: %q", loc)
		}
	})
}

func TestNotConfigured(t *testing.T) {
	e := setup(t)
	e.svc.Cfg.ClientID = ""
	s, m := e.do(t, "POST", "/gmail/connect", nil)
	if s != 503 || m["code"] != "gmail_not_configured" {
		t.Fatalf("%d %v", s, m)
	}
	if s, m := e.do(t, "GET", "/gmail/status", nil); s != 200 || m["configured"] != false {
		t.Fatalf("status when unconfigured: %d %v", s, m)
	}
}

func TestInvalidGrantPausesAndReconnectResumes(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if loc := e.connect(t, "c1"); !strings.Contains(loc, "status=ok") {
		t.Fatal(loc)
	}
	// Two queued write-backs.
	for _, mid := range []string{"<a@x>", "<b@x>"} {
		id := e.insertMessage(t, mid)
		if err := e.st.WantGmailArchived(ctx, []uuid.UUID{id}, true, 0); err != nil {
			t.Fatal(err)
		}
	}
	// Access token is cached: no refresh call yet.
	if tok, err := e.svc.AccessToken(ctx); err != nil || tok != "at-c1" {
		t.Fatalf("cached token: %q %v", tok, err)
	}
	calls := e.fake.TokenCalls()

	// Expire the cache; refresh works once and is cached again.
	e.svc.mu.Lock()
	e.svc.access = nil
	e.svc.mu.Unlock()
	if tok, err := e.svc.AccessToken(ctx); err != nil || tok != "at-refreshed" {
		t.Fatalf("refresh: %q %v", tok, err)
	}
	if _, err := e.svc.AccessToken(ctx); err != nil || e.fake.TokenCalls() != calls+1 {
		t.Fatalf("second call should hit the cache; token calls %d", e.fake.TokenCalls()-calls)
	}

	// Google says the grant is gone.
	e.fake.RevokeGrant()
	e.svc.mu.Lock()
	e.svc.access = nil
	e.svc.mu.Unlock()
	if _, err := e.svc.AccessToken(ctx); err != ErrNeedsReauth {
		t.Fatalf("want ErrNeedsReauth, got %v", err)
	}
	if _, err := e.svc.AccessToken(ctx); err != ErrNeedsReauth {
		t.Fatalf("stays needs_reauth: %v", err)
	}
	_, m := e.do(t, "GET", "/gmail/status", nil)
	if m["state"] != "needs_reauth" || m["waiting"] != float64(2) || m["email"] != "sam@gmail.com" {
		t.Fatalf("status: %v", m)
	}
	var paused int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM gmail_sync WHERE state='paused'`).Scan(&paused)
	if paused != 2 {
		t.Fatalf("paused rows = %d", paused)
	}
	if got := e.ev.types(); got[len(got)-1] != events.TypeGmailStatus {
		t.Fatalf("no gmail.status event: %v", got)
	}
	last := e.ev.ev[len(e.ev.ev)-1].Fields
	if last["state"] != "needs_reauth" {
		t.Fatalf("event fields %v", last)
	}

	// Reconnecting flips paused rows back to pending, due now.
	e.fake.RestoreGrant()
	if loc := e.connect(t, "c2"); !strings.Contains(loc, "status=ok") {
		t.Fatal(loc)
	}
	var pending int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM gmail_sync WHERE state='pending' AND next_attempt_at <= now()`).Scan(&pending)
	if pending != 2 {
		t.Fatalf("pending rows after reconnect = %d", pending)
	}
	if _, m := e.do(t, "GET", "/gmail/status", nil); m["state"] != "connected" {
		t.Fatalf("status: %v", m)
	}
}

func TestReconnectDifferentAccountReplaces(t *testing.T) {
	e := setup(t)
	e.connect(t, "c1")
	e.fake.Email = "other@gmail.com"
	e.connect(t, "c2")
	var n int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM gmail_accounts`).Scan(&n)
	if n != 1 {
		t.Fatalf("accounts = %d", n)
	}
	if _, m := e.do(t, "GET", "/gmail/status", nil); m["email"] != "other@gmail.com" {
		t.Fatalf("%v", m)
	}
}
