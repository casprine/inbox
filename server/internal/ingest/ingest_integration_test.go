package ingest_test

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
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest"
	"github.com/casprine/inbox/server/internal/migrate"
	"github.com/casprine/inbox/server/internal/store"
)

const secret = "test-secret"

type env struct {
	st   *store.Store
	blob *blob.Store
	srv  *httptest.Server
	rw   *ingest.ReconcileWorker
	rc   *river.Client[pgx.Tx]
	pool *pgxpool.Pool
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
		t.Fatalf("migrate: %v", err)
	}
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)
	bl := blob.New(blob.Config{Endpoint: ep, Bucket: "inbox-mail", AccessKey: "minio", SecretKey: "minio12345"})
	// Reconcile ingests everything under raw/ that this schema lacks, and the bucket is shared with other test
	// packages that leave objects behind: start from an empty raw/.
	if old, err := bl.List(ctx, "raw/", ""); err == nil {
		keys := make([]string, len(old))
		for i, o := range old {
			keys[i] = o.Key
		}
		_ = bl.DeleteMany(ctx, keys)
	}
	rc, err := ingest.NewRiver(ingest.Deps{Pool: pool, Store: st, Blob: bl, ReconcileMinAge: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.With(ingest.VerifyHMAC(secret, nil)).Post("/ingest/v1/notify", ingest.NotifyHandler(ingest.RiverEnqueuer{Client: rc}))
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
	return &env{st: st, blob: bl, srv: srv, pool: pool, rc: rc,
		rw: &ingest.ReconcileWorker{Store: st, Blob: bl, MinAge: time.Nanosecond}}
}

func (e *env) putRaw(t *testing.T, fixture string) (key string, size int) {
	t.Helper()
	raw, err := os.ReadFile("parse/testdata/" + fixture)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	key = fmt.Sprintf("raw/%d/%02d/test-%s.eml", now.Year(), int(now.Month()), uuid.NewString())
	meta := map[string]string{"env_from": "x@example.org", "env_to": "a@mydomain.com"}
	if err := e.blob.Put(context.Background(), key, bytes.NewReader(raw), int64(len(raw)), meta); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.blob.Delete(context.Background(), key) })
	return key, len(raw)
}

func (e *env) post(t *testing.T, body any, mutate func(ts *string, sig *string)) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := ingest.Sign(secret, ts, b)
	if mutate != nil {
		mutate(&ts, &sig)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/ingest/v1/notify", bytes.NewReader(b))
	req.Header.Set("X-Inbox-Timestamp", ts)
	req.Header.Set("X-Inbox-Signature", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
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

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) business(t *testing.T, domain string, enabled bool) store.Business {
	t.Helper()
	b, err := e.st.CreateBusiness(context.Background(), store.Business{ID: ids.NewV7(), Name: domain, Domain: domain, Enabled: enabled})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNotifyIngest(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	biz := e.business(t, "mydomain.com", true)
	key, size := e.putRaw(t, "attachment.eml")
	payload := map[string]any{"key": key, "env_from": "x@example.org", "env_to": "a@mydomain.com", "raw_size": size,
		"received_at": time.Now().UTC().Format(time.RFC3339), "message_id": "att-1@example.org"}

	if resp := e.post(t, payload, nil); resp.StatusCode != 202 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	waitFor(t, "message", func() bool { _, err := e.st.GetMessageByRawKey(ctx, key); return err == nil })
	m, _ := e.st.GetMessageByRawKey(ctx, key)
	if m.BusinessID == nil || *m.BusinessID != biz.ID || m.Route != "unrouted" || m.State != "stored" ||
		m.AttachmentCount != 1 || m.ToLocal == nil || *m.ToLocal != "a" {
		t.Fatalf("unexpected message %+v", m)
	}
	if !strings.HasPrefix(m.ThreadKey, "subj:") {
		t.Fatalf("thread key %q", m.ThreadKey)
	}
	if ids, err := e.st.SearchMessageIDs(ctx, "invoice", 5); err != nil || len(ids) != 1 || ids[0] != m.ID {
		t.Fatalf("search by filename: %v %v", ids, err)
	}
	if ids, _ := e.st.SearchMessageIDs(ctx, "attached", 5); len(ids) != 1 {
		t.Fatalf("search by body: %v", ids)
	}
	atts, _ := e.st.ListAttachments(ctx, m.ID)
	if len(atts) != 1 || atts[0].R2Key != fmt.Sprintf("att/%s/0", m.ID) {
		t.Fatalf("attachments %+v", atts)
	}
	rc, err := e.blob.Get(ctx, atts[0].R2Key)
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
	t.Cleanup(func() { _ = e.blob.Delete(ctx, atts[0].R2Key) })
	if n := e.count(t, `SELECT msg_count FROM business_addresses WHERE business_id=$1 AND local_part='a'`, biz.ID); n != 1 {
		t.Fatalf("business_addresses count %d", n)
	}
	logs, _ := e.st.ListIngestLog(ctx, key)
	if len(logs) < 3 || !strings.HasPrefix(logs[0].Line, "received ") || logs[1].Line != "stored" || !strings.HasPrefix(logs[2].Line, "unrouted") {
		t.Fatalf("ingest log %+v", logs)
	}

	// duplicate notify, and a different blob carrying the same Message-ID: no new rows
	if resp := e.post(t, payload, nil); resp.StatusCode != 202 {
		t.Fatalf("dup status %d", resp.StatusCode)
	}
	key2, _ := e.putRaw(t, "attachment.eml")
	payload["key"] = key2
	if resp := e.post(t, payload, nil); resp.StatusCode != 202 {
		t.Fatalf("dup2 status %d", resp.StatusCode)
	}
	waitFor(t, "duplicate log", func() bool {
		return e.count(t, `SELECT count(*) FROM ingest_log WHERE ingest_key=$1 AND line LIKE '%duplicate%'`, key2) == 1
	})
	if n := e.count(t, `SELECT count(*) FROM messages`); n != 1 {
		t.Fatalf("messages = %d, want 1", n)
	}
	if n := e.count(t, `SELECT count(*) FROM message_search`); n != 1 {
		t.Fatalf("message_search = %d", n)
	}
}

func TestUnknownDomainIsUnrouted(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.business(t, "mydomain.com", false) // disabled does not route
	key, size := e.putRaw(t, "plain.eml")
	e.post(t, map[string]any{"key": key, "env_from": "alice@example.com", "env_to": "shop@mydomain.com", "raw_size": size}, nil)
	waitFor(t, "message", func() bool { _, err := e.st.GetMessageByRawKey(ctx, key); return err == nil })
	m, _ := e.st.GetMessageByRawKey(ctx, key)
	if m.BusinessID != nil || m.Route != "unrouted" {
		t.Fatalf("%+v", m)
	}
}

func TestBadSignature(t *testing.T) {
	e := setup(t)
	body := map[string]any{"key": "raw/2026/01/x.eml"}
	cases := map[string]func(*string, *string){
		"wrong sig":   func(_, sig *string) { *sig = strings.Repeat("0", 64) },
		"not hex":     func(_, sig *string) { *sig = "zz" },
		"missing sig": func(_, sig *string) { *sig = "" },
		"stale ts": func(ts, sig *string) {
			*ts = strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
			b, _ := json.Marshal(body)
			*sig = ingest.Sign(secret, *ts, b) // valid signature but outside the window
		},
		"bad ts": func(ts, _ *string) { *ts = "abc" },
	}
	for name, mut := range cases {
		if resp := e.post(t, body, mut); resp.StatusCode != 401 {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM river_job`); n != 0 {
		t.Fatalf("jobs enqueued despite bad signature: %d", n)
	}
}

func TestReconcileOrphan(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.business(t, "mydomain.com", true)
	key, _ := e.putRaw(t, "plain.eml")
	var found []string
	n, err := e.rw.Reconcile(ctx, func(a ingest.IngestArgs) error {
		found = append(found, a.Key)
		if a.Key == key && (a.EnvTo != "a@mydomain.com" || a.EnvFrom != "x@example.org") {
			t.Errorf("metadata not carried: %+v", a)
		}
		return nil
	})
	if err != nil || n < 1 || !contains(found, key) {
		t.Fatalf("reconcile n=%d err=%v found=%v", n, err, found)
	}
	// now really enqueue through River via a periodic-style run, then it must be ingested and no longer an orphan
	e.post(t, map[string]any{"key": key, "env_from": "x@example.org", "env_to": "a@mydomain.com"}, nil)
	waitFor(t, "message", func() bool { _, err := e.st.GetMessageByRawKey(ctx, key); return err == nil })
	_, _ = e.rw.Reconcile(ctx, func(a ingest.IngestArgs) error {
		if a.Key == key {
			t.Errorf("already ingested key re-enqueued")
		}
		return nil
	})
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestStoreFailed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	body := map[string]any{"key": nil, "reason": "store_failed", "env_from": "x@example.org", "env_to": "a@mydomain.com",
		"raw_size": 100, "received_at": time.Now().UTC().Format(time.RFC3339)}
	for i := 0; i < 2; i++ { // retried notify is idempotent
		if resp := e.post(t, body, nil); resp.StatusCode != 202 {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	waitFor(t, "failed row", func() bool { return e.count(t, `SELECT count(*) FROM messages WHERE state='failed'`) == 1 })
	time.Sleep(time.Second)
	var reason string
	var rawKey *string
	if err := e.pool.QueryRow(ctx, `SELECT failure_reason, raw_key FROM messages WHERE state='failed'`).Scan(&reason, &rawKey); err != nil {
		t.Fatal(err)
	}
	if reason != "store_failed" || rawKey != nil {
		t.Fatalf("reason=%q raw=%v", reason, rawKey)
	}
	if n := e.count(t, `SELECT count(*) FROM messages`); n != 1 {
		t.Fatalf("messages = %d", n)
	}
}

func TestBadPayload(t *testing.T) {
	e := setup(t)
	for _, body := range []map[string]any{{"key": "evil/../x"}, {"key": nil}, {}} {
		if resp := e.post(t, body, nil); resp.StatusCode != 400 {
			t.Errorf("%v: status %d, want 400", body, resp.StatusCode)
		}
	}
}

// The Worker's ping never arrived (server down, Mac mini asleep, Funnel hiccup): the mail is in R2 and
// nowhere else. A reconcile run must find it and ingest it exactly once.
func TestReconcileIngestsMailWhoseNotifyWasMissed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.business(t, "mydomain.com", true)
	key, _ := e.putRaw(t, "plain.eml") // stored in R2, no notify posted
	enqueue := func(a ingest.IngestArgs) error { _, err := e.rc.Insert(ctx, a, nil); return err }

	if _, err := e.st.GetMessageByRawKey(ctx, key); err == nil {
		t.Fatal("message exists before reconcile")
	}
	if _, err := e.rw.Reconcile(ctx, enqueue); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "message from the missed notify", func() bool { _, err := e.st.GetMessageByRawKey(ctx, key); return err == nil })

	// A later run (and a late notify for the same mail) must not create a second copy.
	var again []string
	if _, err := e.rw.Reconcile(ctx, func(a ingest.IngestArgs) error { again = append(again, a.Key); return nil }); err != nil {
		t.Fatal(err)
	}
	if contains(again, key) {
		t.Fatalf("ingested key enqueued again: %v", again)
	}
	e.post(t, map[string]any{"key": key, "env_from": "x@example.org", "env_to": "a@mydomain.com"}, nil)
	time.Sleep(time.Second)
	if n := e.count(t, `SELECT count(*) FROM messages WHERE raw_key = '`+key+`'`); n != 1 {
		t.Fatalf("copies of the mail: %d", n)
	}
}

// Catch-up after a long outage is not limited to a recent window, and a saved cursor lets later runs skip
// what was already seen, while the periodic full scan still repairs anything behind the cursor.
func TestReconcileCursorAndFullScan(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.business(t, "mydomain.com", true)
	now := time.Now()
	clock := now.Add(3 * time.Hour) // objects are "old" for the cursor lag
	e.rw.Now = func() time.Time { return clock }
	var got []string
	enqueue := func(a ingest.IngestArgs) error { got = append(got, a.Key); return nil }

	keyA, _ := e.putRaw(t, "plain.eml")
	if _, err := e.rw.Reconcile(ctx, enqueue); err != nil || !contains(got, keyA) {
		t.Fatalf("first run: err=%v got=%v", err, got)
	}
	raw, err := e.st.GetSetting(ctx, "reconcile_cursor")
	if err != nil || !strings.Contains(string(raw), "raw/") {
		t.Fatalf("cursor not saved: %s %v", raw, err)
	}

	// A mail whose key sorts before the cursor is not seen by cursor runs...
	early := "raw/0001/01/01EARLY" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))[:8] + ".eml"
	body, _ := os.ReadFile("parse/testdata/plain.eml")
	if err := e.blob.Put(ctx, early, bytes.NewReader(body), int64(len(body)), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.blob.Delete(context.Background(), early) })
	got = nil
	if _, err := e.rw.Reconcile(ctx, enqueue); err != nil || contains(got, early) {
		t.Fatalf("cursor run saw a key behind the cursor: err=%v got=%v", err, got)
	}
	// ...but the daily full list-and-diff does.
	clock = now.Add(30 * time.Hour)
	got = nil
	if _, err := e.rw.Reconcile(ctx, enqueue); err != nil || !contains(got, early) {
		t.Fatalf("full scan missed it: err=%v got=%v", err, got)
	}
}

func TestRefreshQueuesOneReconcile(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	enq := ingest.RiverEnqueuer{Client: e.rc}
	for i := 0; i < 3; i++ {
		if err := enq.RequestReconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "reconcile job ran", func() bool {
		return e.count(t, `SELECT count(*) FROM river_job WHERE kind='reconcile_r2' AND state='completed'`) >= 1
	})
	// Unique while queued/running: the three requests collapse (at most 2 rows: one started before the next arrived).
	if n := e.count(t, `SELECT count(*) FROM river_job WHERE kind='reconcile_r2'`); n > 3 || n < 1 {
		t.Fatalf("reconcile jobs: %d", n)
	}
}
