package mail_test

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
	"github.com/casprine/inbox/server/internal/events"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/mail"
	"github.com/casprine/inbox/server/internal/migrate"
	"github.com/casprine/inbox/server/internal/store"
)

type env struct {
	st    *store.Store
	blob  *blob.Store
	srv   *httptest.Server
	token string
	hub   *events.Hub
	retry *fakeRetry
}

type fakeRetry struct {
	keys []string
	err  error
}

func (f *fakeRetry) RetryIngest(_ context.Context, key, _, _ string, _ time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.keys = append(f.keys, key)
	return nil
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

	// Mint a device the same way auth issues one, then use the real bearer middleware.
	token := "test-token-" + uuid.NewString()
	hash, _ := hexDecode(crypto.HashToken(token))
	if err := st.CreateDevice(ctx, ids.NewV7(), "test", hash); err != nil {
		t.Fatal(err)
	}
	svc := &auth.Service{Store: st, RateLimit: 1000, RateBurst: 1000}
	fr := &fakeRetry{}
	api := &mail.API{Store: st, Blob: bl, Retry: fr}
	hub := events.NewHub(pool, st, nil)
	hub.Heartbeat = 150 * time.Millisecond
	hctx, hcancel := context.WithCancel(ctx)
	go hub.Run(hctx)
	r := chi.NewRouter()
	svc.Routes(r, func(r chi.Router) {
		api.Routes(r)
		r.Get("/events", hub.ServeHTTP)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(func() {
		hcancel()
		srv.Close()
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return &env{st: st, blob: bl, srv: srv, token: token, hub: hub, retry: fr}
}

func hexDecode(s string) ([]byte, error) { return hex.DecodeString(s) }

func (e *env) do(t *testing.T, method, path string, body any) (int, []byte, http.Header) {
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
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}

func decodeInto(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
}

type msgs struct {
	Messages []struct {
		ID         string  `json:"id"`
		BusinessID *string `json:"business_id"`
		IsRead     bool    `json:"is_read"`
		Archived   bool    `json:"archived"`
		Subject    *string `json:"subject"`
	} `json:"messages"`
	NextCursor *string `json:"next_cursor"`
}

type bizList struct {
	Businesses []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Unread    int    `json:"unread"`
		Hidden    bool   `json:"hidden"`
		Addresses []struct {
			LocalPart string `json:"local_part"`
		} `json:"addresses"`
		LastMessageAt *string `json:"last_message_at"`
	} `json:"businesses"`
}

func TestAuthRequired(t *testing.T) {
	e := setup(t)
	resp, _ := http.Get(e.srv.URL + "/v1/businesses")
	if resp.StatusCode != 401 {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestBusinessesCRUD(t *testing.T) {
	e := setup(t)
	st, body, _ := e.do(t, "POST", "/businesses", map[string]any{"name": "Acme", "domain": "Acme.COM"})
	if st != 201 {
		t.Fatalf("create %d %s", st, body)
	}
	var a struct{ ID, Domain string }
	decodeInto(t, body, &a)
	if a.Domain != "acme.com" {
		t.Fatal(a.Domain)
	}
	if st, body, _ = e.do(t, "POST", "/businesses", map[string]any{"name": "Dup", "domain": "acme.com"}); st != 409 || !strings.Contains(string(body), "domain_taken") {
		t.Fatalf("dup %d %s", st, body)
	}
	if st, _, _ = e.do(t, "POST", "/businesses", map[string]any{"name": "", "domain": "x"}); st != 400 {
		t.Fatal(st)
	}
	_, body, _ = e.do(t, "POST", "/businesses", map[string]any{"name": "Beta", "domain": "beta.io"})
	var b struct{ ID string }
	decodeInto(t, body, &b)

	if st, body, _ = e.do(t, "PATCH", "/businesses/"+a.ID, map[string]any{"hidden": true, "name": "Acme 2"}); st != 200 {
		t.Fatalf("patch %d %s", st, body)
	}
	if st, _, _ = e.do(t, "PATCH", "/businesses/"+uuid.NewString(), map[string]any{"hidden": true}); st != 404 {
		t.Fatal(st)
	}
	if st, body, _ = e.do(t, "PUT", "/businesses:order", map[string]any{"ids": []string{b.ID, a.ID}}); st != 200 {
		t.Fatalf("order %d %s", st, body)
	}
	var l bizList
	decodeInto(t, body, &l)
	if len(l.Businesses) != 2 || l.Businesses[0].ID != b.ID || !l.Businesses[1].Hidden || l.Businesses[1].Name != "Acme 2" {
		t.Fatalf("%+v", l)
	}
	if st, _, _ = e.do(t, "DELETE", "/businesses/"+b.ID, nil); st != 204 {
		t.Fatal(st)
	}
	if st, _, _ = e.do(t, "DELETE", "/businesses/"+b.ID, nil); st != 404 {
		t.Fatal(st)
	}
}

func TestSeedMessagesFlow(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	n, err := mail.SeedDemo(ctx, e.st, e.blob, nil)
	if err != nil || n == 0 {
		t.Fatalf("seed %d %v", n, err)
	}
	if _, err := mail.SeedDemo(ctx, e.st, e.blob, nil); err != nil { // idempotent
		t.Fatal(err)
	}

	_, body, _ := e.do(t, "GET", "/businesses", nil)
	var bl bizList
	decodeInto(t, body, &bl)
	if len(bl.Businesses) != 3 {
		t.Fatalf("%+v", bl)
	}
	total := 0
	for _, b := range bl.Businesses {
		total += b.Unread
		if b.Unread > 0 && (len(b.Addresses) == 0 || b.LastMessageAt == nil) {
			t.Fatalf("missing addresses/last_message_at: %+v", b)
		}
	}
	if total == 0 {
		t.Fatal("no unread")
	}

	// list: failed excluded; paginate with limit 2 and compare to the full list
	_, body, _ = e.do(t, "GET", "/messages?limit=200", nil)
	var all msgs
	decodeInto(t, body, &all)
	if len(all.Messages) < 4 || all.NextCursor != nil {
		t.Fatalf("all=%d cursor=%v", len(all.Messages), all.NextCursor)
	}
	var paged []string
	cursor := ""
	for i := 0; i < 20; i++ {
		_, body, _ = e.do(t, "GET", "/messages?limit=2&cursor="+cursor, nil)
		var pg msgs
		decodeInto(t, body, &pg)
		for _, m := range pg.Messages {
			paged = append(paged, m.ID)
		}
		if pg.NextCursor == nil {
			break
		}
		cursor = *pg.NextCursor
	}
	if len(paged) != len(all.Messages) {
		t.Fatalf("paged %d vs %d", len(paged), len(all.Messages))
	}
	for i, m := range all.Messages {
		if paged[i] != m.ID {
			t.Fatalf("order differs at %d", i)
		}
	}
	if st, _, _ := e.do(t, "GET", "/messages?cursor=!!", nil); st != 400 {
		t.Fatal(st)
	}

	// filters
	biz := bl.Businesses[0].ID
	_, body, _ = e.do(t, "GET", "/messages?business="+biz, nil)
	var fb msgs
	decodeInto(t, body, &fb)
	for _, m := range fb.Messages {
		if m.BusinessID == nil || *m.BusinessID != biz {
			t.Fatal("business filter leaked")
		}
	}

	// detail + body + raw
	first := all.Messages[0].ID
	st, body, _ := e.do(t, "GET", "/messages/"+first, nil)
	if st != 200 {
		t.Fatalf("detail %d %s", st, body)
	}
	var det struct {
		Message map[string]any   `json:"message"`
		Thread  []map[string]any `json:"thread"`
		Details map[string]any   `json:"details"`
	}
	decodeInto(t, body, &det)
	if len(det.Thread) == 0 || det.Thread[0]["direction"] != "in" || det.Details["received_at"] == nil {
		t.Fatalf("%s", body)
	}
	if st, body, _ = e.do(t, "GET", "/messages/"+first+"/body", nil); st != 200 {
		t.Fatalf("body %d %s", st, body)
	}
	var bd map[string]any
	decodeInto(t, body, &bd)
	if _, ok := bd["remote_image_count"]; !ok || bd["auth_results"] == nil || (bd["html"] == nil && bd["text"] == nil) {
		t.Fatalf("%s", body)
	}
	st, raw, h := e.do(t, "GET", "/messages/"+first+"/raw", nil)
	if st != 200 || h.Get("Content-Type") != "message/rfc822" || !bytes.Contains(raw, []byte("Message-ID")) && !bytes.Contains(raw, []byte("Subject")) {
		t.Fatalf("raw %d %s", st, h.Get("Content-Type"))
	}
	if st, _, _ = e.do(t, "GET", "/messages/"+uuid.NewString(), nil); st != 404 {
		t.Fatal(st)
	}

	// attachment redirect
	var attID string
	if err := e.st.Pool().QueryRow(ctx, `SELECT id FROM attachments LIMIT 1`).Scan(&attID); err != nil {
		t.Fatal(err)
	}
	st, _, h = e.do(t, "GET", "/attachments/"+attID, nil)
	if st != 302 || !strings.Contains(h.Get("Location"), "X-Amz-Expires=60") {
		t.Fatalf("att %d %q", st, h.Get("Location"))
	}
	resp, err := http.Get(h.Get("Location"))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("presigned fetch %v", err)
	}
	resp.Body.Close()
	var attMsg string
	_ = e.st.Pool().QueryRow(ctx, `SELECT message_id FROM attachments WHERE id=$1`, attID).Scan(&attMsg)
	_, body, _ = e.do(t, "GET", "/messages/"+attMsg, nil)
	if !strings.Contains(string(body), attID) {
		t.Fatal("attachment missing from detail")
	}

	// batch
	before := bl.Businesses
	_ = before
	if st, body, _ = e.do(t, "POST", "/messages:batch", map[string]any{"ids": []string{first}, "op": "read"}); st != 200 {
		t.Fatalf("%d %s", st, body)
	}
	var br msgs
	decodeInto(t, body, &br)
	if len(br.Messages) != 1 || !br.Messages[0].IsRead {
		t.Fatalf("%s", body)
	}
	_, body, _ = e.do(t, "GET", "/messages?unread=true&limit=200", nil)
	var un msgs
	decodeInto(t, body, &un)
	if len(un.Messages) != len(all.Messages)-1 {
		t.Fatalf("unread filter %d", len(un.Messages))
	}
	e.do(t, "POST", "/messages:batch", map[string]any{"ids": []string{first}, "op": "unread"})

	start := time.Now()
	if st, body, _ = e.do(t, "POST", "/messages:batch", map[string]any{"ids": []string{first}, "op": "archive"}); st != 200 {
		t.Fatalf("%d %s", st, body)
	}
	decodeInto(t, body, &br)
	if !br.Messages[0].Archived {
		t.Fatal("not archived")
	}
	var wantArch bool
	var next time.Time
	if err := e.st.Pool().QueryRow(ctx, `SELECT want_archived, next_attempt_at FROM gmail_sync WHERE message_id=$1`, first).Scan(&wantArch, &next); err != nil {
		t.Fatal(err)
	}
	if !wantArch || next.Before(start.Add(7*time.Second)) || next.After(time.Now().Add(10*time.Second)) {
		t.Fatalf("gmail_sync %v %v", wantArch, next)
	}
	_, body, _ = e.do(t, "GET", "/messages?limit=200", nil)
	decodeInto(t, body, &un)
	if len(un.Messages) != len(all.Messages)-1 {
		t.Fatal("archived still listed")
	}
	e.do(t, "POST", "/messages:batch", map[string]any{"ids": []string{first}, "op": "unarchive"})
	_ = e.st.Pool().QueryRow(ctx, `SELECT want_archived FROM gmail_sync WHERE message_id=$1`, first).Scan(&wantArch)
	if wantArch {
		t.Fatal("want_archived not cleared")
	}
	if st, _, _ = e.do(t, "POST", "/messages:batch", map[string]any{"ids": []string{first}, "op": "nope"}); st != 400 {
		t.Fatal(st)
	}
	if st, _, _ = e.do(t, "POST", "/messages:batch", map[string]any{"ids": []string{}, "op": "read"}); st != 400 {
		t.Fatal(st)
	}

	// delete business purges rows and blobs
	var rawKey string
	if err := e.st.Pool().QueryRow(ctx, `SELECT raw_key FROM messages WHERE id=$1`, attMsg).Scan(&rawKey); err != nil {
		t.Fatal(err)
	}
	var attKey, attBiz string
	if err := e.st.Pool().QueryRow(ctx, `SELECT a.r2_key, m.business_id::text FROM attachments a JOIN messages m ON m.id=a.message_id WHERE a.id=$1`, attID).Scan(&attKey, &attBiz); err != nil {
		t.Fatal(err)
	}
	if st, _, _ = e.do(t, "DELETE", "/businesses/"+attBiz, nil); st != 204 {
		t.Fatal(st)
	}
	var cnt int
	_ = e.st.Pool().QueryRow(ctx, `SELECT count(*) FROM messages WHERE id=$1`, attMsg).Scan(&cnt)
	if cnt != 0 {
		t.Fatal("messages remain")
	}
	for _, k := range []string{rawKey, attKey} {
		if _, err := e.blob.Head(ctx, k); err == nil {
			t.Fatalf("blob %s remains", k)
		}
	}
}

func TestSeedDemoCoversTheLiveTestScenarios(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := mail.SeedDemo(ctx, e.st, e.blob, nil); err != nil {
		t.Fatal(err)
	}
	count := func(sql string) int {
		var n int
		if err := e.st.Pool().QueryRow(ctx, sql).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count(`SELECT count(*) FROM messages`) + count(`SELECT count(*) FROM rules`)*1000
	if _, err := mail.SeedDemo(ctx, e.st, e.blob, nil); err != nil { // idempotent
		t.Fatal(err)
	}
	after := count(`SELECT count(*) FROM messages`) + count(`SELECT count(*) FROM rules`)*1000
	if before != after {
		t.Fatalf("re-seeding changed the data: %d -> %d", before, after)
	}

	// Failed items: store_failed without a copy, parse_failed, and one store_failed whose copy is intact.
	_, body, _ := e.do(t, "GET", "/attention/failed", nil)
	var failed struct {
		Messages []struct {
			ID            string  `json:"id"`
			FailureReason *string `json:"failure_reason"`
			HasRaw        bool    `json:"has_raw"`
		} `json:"messages"`
	}
	decodeInto(t, body, &failed)
	got := map[string]int{}
	for _, m := range failed.Messages {
		k := *m.FailureReason
		if m.HasRaw {
			k += "+raw"
		}
		got[k]++
	}
	if len(failed.Messages) != 3 || got["store_failed"] != 1 || got["store_failed+raw"] != 1 || got["parse_failed+raw"] != 1 {
		t.Fatalf("failed items: %v", got)
	}

	// A conversation with a reply you sent.
	var thread string
	if err := e.st.Pool().QueryRow(ctx, `SELECT id::text FROM messages WHERE message_id_hdr='seed-thread-1@client.test'`).Scan(&thread); err != nil {
		t.Fatal(err)
	}
	_, body, _ = e.do(t, "GET", "/messages/"+thread, nil)
	var det struct {
		Thread []struct {
			Direction string `json:"direction"`
		} `json:"thread"`
	}
	decodeInto(t, body, &det)
	if len(det.Thread) != 3 || det.Thread[0].Direction != "in" || det.Thread[1].Direction != "out" || det.Thread[2].Direction != "in" {
		t.Fatalf("thread: %+v", det.Thread)
	}

	// Mail with remote images, mail with attachments, mail that matched a rule, mail that needs one.
	var img string
	if err := e.st.Pool().QueryRow(ctx, `SELECT id::text FROM messages WHERE message_id_hdr='seed-images-1@promo.test'`).Scan(&img); err != nil {
		t.Fatal(err)
	}
	_, body, _ = e.do(t, "GET", "/messages/"+img+"/body", nil)
	var mb struct {
		Remote int `json:"remote_image_count"`
	}
	decodeInto(t, body, &mb)
	if mb.Remote != 2 {
		t.Fatalf("remote images: %d", mb.Remote)
	}
	if count(`SELECT count(*) FROM messages WHERE attachment_count > 0`) == 0 ||
		count(`SELECT count(*) FROM messages WHERE route='routed' AND rule_id IS NOT NULL`) == 0 ||
		count(`SELECT count(*) FROM messages WHERE route='unrouted' AND state='stored' AND direction='in'`) == 0 {
		t.Fatal("seed lacks attachments, routed or unrouted mail")
	}

	// A paused import waiting out a rate limit.
	bf, err := e.st.ListBackfills(ctx)
	if err != nil || len(bf) != 1 || bf[0].Status != "paused" || bf[0].PausedUntil == nil || bf[0].LastError == nil || *bf[0].LastError != "rate_limited" {
		t.Fatalf("backfills: %+v %v", bf, err)
	}
}
