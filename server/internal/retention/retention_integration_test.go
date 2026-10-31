package retention_test

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

	"github.com/casprine/inbox/server/internal/auth"
	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest"
	"github.com/casprine/inbox/server/internal/ingest/parse"
	"github.com/casprine/inbox/server/internal/mail"
	"github.com/casprine/inbox/server/internal/retention"
	"github.com/casprine/inbox/server/internal/store"
	"github.com/casprine/inbox/server/internal/testutil"
)

type env struct {
	db   *testutil.DB
	blob *blob.Store
	ing  *ingest.Worker
	srv  *httptest.Server
	api  *retention.API
	now  time.Time
}

func setup(t *testing.T) *env {
	t.Helper()
	ep := os.Getenv("TEST_S3_ENDPOINT")
	if ep == "" {
		t.Skip("TEST_S3_ENDPOINT not set")
	}
	db := testutil.NewDB(t)
	bl := blob.New(blob.Config{Endpoint: ep, Bucket: "inbox-mail", AccessKey: "minio", SecretKey: "minio12345"})
	e := &env{db: db, blob: bl, now: time.Now().UTC()}
	e.ing = &ingest.Worker{Store: db.Store, Blob: bl, Router: ingest.RulesRouter{}}
	e.api = &retention.API{Store: db.Store, Blob: bl, Now: func() time.Time { return e.now }}
	mapi := &mail.API{Store: db.Store, Blob: bl}
	asvc := &auth.Service{Store: db.Store, RateLimit: 1000, RateBurst: 1000}
	r := chi.NewRouter()
	asvc.Routes(r, func(r chi.Router) { e.api.Routes(r); mapi.Routes(r) })
	e.srv = httptest.NewServer(r)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
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

func (e *env) biz(t *testing.T, domain string) store.Business {
	t.Helper()
	b, err := e.db.Store.CreateBusiness(context.Background(), store.Business{ID: ids.NewV7(), Name: domain, Domain: domain, CFZoneID: "z", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// emlWithAttachment builds a multipart message with one attachment.
func emlWithAttachment(msgID, to string) []byte {
	return []byte(fmt.Sprintf("From: Pat <pat@client.com>\r\nTo: %s\r\nMessage-ID: <%s>\r\nSubject: %s\r\nDate: Mon, 02 Mar 2026 10:00:00 +0000\r\nMIME-Version: 1.0\r\n"+
		"Content-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\nContent-Type: text/plain\r\n\r\nhello\r\n--B\r\nContent-Type: application/pdf; name=\"inv.pdf\"\r\n"+
		"Content-Disposition: attachment; filename=\"inv.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n--B--\r\n",
		to, msgID, msgID, base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 fake "+msgID))))
}

type stored struct {
	id       uuid.UUID
	rawKey   string
	attKeys  []string
	received time.Time
}

// ingestMail stores a message received `age` ago through the real ingest path (raw blob + attachment blob).
func (e *env) ingestMail(t *testing.T, msgID, to string, age time.Duration) stored {
	t.Helper()
	ctx := context.Background()
	raw := emlWithAttachment(msgID, to)
	key := "raw/2026/03/" + msgID + ".eml"
	if err := e.blob.Put(ctx, key, bytes.NewReader(raw), int64(len(raw)), nil); err != nil {
		t.Fatal(err)
	}
	p, err := parse.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Add(-age)
	res, err := e.ing.Ingest(ctx, p, ingest.RawOpts{Key: key, EnvTo: to, ReceivedAt: when, Size: len(raw), Source: "edge"})
	if err != nil || !res.Inserted {
		t.Fatalf("ingest %s: %+v %v", msgID, res, err)
	}
	rows, err := e.db.Pool.Query(ctx, `SELECT r2_key FROM attachments WHERE message_id=$1`, res.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	s := stored{id: res.MessageID, rawKey: key, received: when}
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		s.attKeys = append(s.attKeys, k)
	}
	if len(s.attKeys) != 1 {
		t.Fatalf("expected one attachment, got %v", s.attKeys)
	}
	return s
}

func (e *env) exists(t *testing.T, key string) bool {
	t.Helper()
	_, err := e.blob.Head(context.Background(), key)
	return err == nil
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.db.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func day(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

func TestPrefs(t *testing.T) {
	e := setup(t)
	s, m := e.do(t, "GET", "/prefs", nil)
	if s != 200 || m["retention_days"] != nil || m["preview_lines"] != float64(2) || len(m["image_allow_senders"].([]any)) != 0 {
		t.Fatalf("defaults: %d %v", s, m)
	}
	s, m = e.do(t, "PUT", "/prefs", map[string]any{"preview_lines": 3})
	if s != 200 || m["preview_lines"] != float64(3) || m["retention_days"] != nil {
		t.Fatalf("%d %v", s, m)
	}
	s, m = e.do(t, "PUT", "/prefs", map[string]any{"retention_days": 90, "image_allow_senders": []string{" News@Example.com ", "@shop.com", "news@example.com", "other.org"}})
	if s != 200 || m["retention_days"] != float64(90) || m["preview_lines"] != float64(3) {
		t.Fatalf("%d %v", s, m)
	}
	got := fmt.Sprint(m["image_allow_senders"])
	if got != "[@shop.com news@example.com other.org]" {
		t.Fatalf("senders normalised/deduped/sorted: %s", got)
	}
	// Omitted fields persist; explicit null means keep forever.
	if _, m = e.do(t, "GET", "/prefs", nil); m["retention_days"] != float64(90) {
		t.Fatalf("%v", m)
	}
	if s, m = e.do(t, "PUT", "/prefs", `{"retention_days": null}`); s != 200 || m["retention_days"] != nil || m["preview_lines"] != float64(3) {
		t.Fatalf("null: %d %v", s, m)
	}
	if s, m = e.do(t, "PUT", "/prefs", `{}`); s != 200 || m["preview_lines"] != float64(3) {
		t.Fatalf("empty: %d %v", s, m)
	}
	for body, field := range map[string]string{
		`{"retention_days": 3}`:                  "retention_days",
		`{"retention_days": 99999}`:              "retention_days",
		`{"preview_lines": 0}`:                   "preview_lines",
		`{"preview_lines": 5}`:                   "preview_lines",
		`{"image_allow_senders": ["not valid"]}`: "image_allow_senders",
		`{"image_allow_senders": ["a@b@c.com"]}`: "image_allow_senders",
		`{"image_allow_senders": [""]}`:          "image_allow_senders",
		`{"image_allow_senders": ["bare"]}`:      "image_allow_senders",
	} {
		if s, m := e.do(t, "PUT", "/prefs", body); s != 422 || m["code"] != "invalid_prefs" || m["field"] != field {
			t.Errorf("%s: %d %v", body, s, m)
		}
	}
	for _, body := range []string{`{"nope": 1}`, `not json`, `{"retention_days": "x"}`} {
		if s, m := e.do(t, "PUT", "/prefs", body); s != 400 || m["code"] != "invalid_body" {
			t.Errorf("%s: %d %v", body, s, m)
		}
	}
	// Bad updates changed nothing.
	if _, m = e.do(t, "GET", "/prefs", nil); m["retention_days"] != nil || m["preview_lines"] != float64(3) {
		t.Fatalf("%v", m)
	}
}

func TestPruneAppliesRetention(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.biz(t, "acme.com")
	fresh := e.ingestMail(t, "fresh@x", "hello@acme.com", day(5))
	mid := e.ingestMail(t, "mid@x", "hello@acme.com", day(40))
	old := e.ingestMail(t, "old@x", "hello@acme.com", day(120))
	pr := &retention.Pruner{Store: e.db.Store, Blob: e.blob}

	// Keep forever by default: nothing is deleted.
	if res, err := pr.Run(ctx); err != nil || res.Messages != 0 {
		t.Fatalf("default retention deleted mail: %+v %v", res, err)
	}
	if e.count(t, `SELECT count(*) FROM messages`) != 3 {
		t.Fatal("messages gone")
	}

	d := 30
	if err := retention.Save(ctx, e.db.Store, retention.Prefs{RetentionDays: &d, ImageAllowSenders: []string{}, PreviewLines: 2}); err != nil {
		t.Fatal(err)
	}
	// Housekeeping rows that should go too.
	db := e.db.Pool
	_, _ = db.Exec(ctx, `INSERT INTO idempotency_keys (key, device_id, at) VALUES ('old', $1, now() - interval '3 days'), ('new', $1, now())`, e.db.DeviceID)
	_, _ = db.Exec(ctx, `INSERT INTO ingest_log (ingest_key, level, line, at) VALUES ('x','info','orphan old', now() - interval '45 days'), ('y','info','orphan new', now())`)
	_ = e.db.Store.CreateGmailOAuthState(ctx, "stale", nil, "v", -time.Minute)

	res, err := pr.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Messages != 2 || res.Blobs != 4 || res.IdempotencyKeys != 1 || res.LogLines != 1 {
		t.Fatalf("result %+v", res)
	}
	for _, s := range []stored{mid, old} {
		if e.count(t, `SELECT count(*) FROM messages WHERE id=$1`, s.id) != 0 || e.exists(t, s.rawKey) || e.exists(t, s.attKeys[0]) {
			t.Fatalf("message older than 30 days not fully removed: %+v", s)
		}
		if e.count(t, `SELECT count(*) FROM attachments WHERE message_id=$1`, s.id)+e.count(t, `SELECT count(*) FROM message_search WHERE message_id=$1`, s.id) != 0 {
			t.Fatal("dependent rows remain")
		}
	}
	if e.count(t, `SELECT count(*) FROM messages WHERE id=$1`, fresh.id) != 1 || !e.exists(t, fresh.rawKey) || !e.exists(t, fresh.attKeys[0]) {
		t.Fatal("recent message was damaged")
	}
	if e.count(t, `SELECT count(*) FROM idempotency_keys`) != 1 || e.count(t, `SELECT count(*) FROM ingest_log WHERE line='orphan new'`) != 1 ||
		e.count(t, `SELECT count(*) FROM gmail_oauth_states`) != 0 {
		t.Fatal("housekeeping wrong")
	}
	// Idempotent.
	if res, err := pr.Run(ctx); err != nil || res.Messages != 0 {
		t.Fatalf("second run %+v %v", res, err)
	}
}

func TestPruneBatches(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	biz := e.biz(t, "acme.com")
	if _, err := e.db.Pool.Exec(ctx, `INSERT INTO messages (id, business_id, source, message_id_hdr, thread_key, received_at)
SELECT gen_random_uuid(), $1, 'edge', 'm'||g, 't'||g, now() - interval '400 days' FROM generate_series(1, 450) g`, biz.ID); err != nil {
		t.Fatal(err)
	}
	d := 365
	_ = retention.Save(ctx, e.db.Store, retention.Prefs{RetentionDays: &d, ImageAllowSenders: []string{}, PreviewLines: 2})
	res, err := (&retention.Pruner{Store: e.db.Store}).Run(ctx)
	if err != nil || res.Messages != 450 || e.count(t, `SELECT count(*) FROM messages`) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestScheduleIsDailyAt0300UTC(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 3, 10, h, m, 0, 0, time.UTC) }
	for _, tc := range []struct{ from, want time.Time }{
		{at(2, 0), at(3, 0)},
		{at(3, 0), at(3, 0).AddDate(0, 0, 1)},
		{at(12, 30), at(3, 0).AddDate(0, 0, 1)},
		{at(23, 59), at(3, 0).AddDate(0, 0, 1)},
	} {
		if got := retention.NextRun(tc.from); !got.Equal(tc.want) {
			t.Errorf("after %v: %v want %v", tc.from, got, tc.want)
		}
	}
}

func TestDeleteAllDataNeedsConfirmation(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	acme := e.biz(t, "acme.com")
	m1 := e.ingestMail(t, "a@x", "hello@acme.com", day(1))
	m2 := e.ingestMail(t, "b@x", "hello@acme.com", day(2))
	orphan := "raw/2026/03/orphan.eml"
	if err := e.blob.Put(ctx, orphan, strings.NewReader("orphan"), 6, nil); err != nil {
		t.Fatal(err)
	}
	gm := "gmail/zzz.eml"
	_ = e.blob.Put(ctx, gm, strings.NewReader("g"), 1, nil)
	lbl := "L"
	_, _ = e.db.Pool.Exec(ctx, `INSERT INTO rules (id, name, position, business_id, gmail_label) VALUES ($1,'r',0,$2,$3)`, ids.NewV7(), acme.ID, lbl)
	_, _ = e.db.Pool.Exec(ctx, `INSERT INTO backfills (id, business_id, since, status) VALUES ($1,$2,now(),'done'), ($3,$2,now(),'paused')`, ids.NewV7(), acme.ID, ids.NewV7())
	_ = e.db.Store.SetSetting(ctx, "prefs", retention.Prefs{PreviewLines: 3, ImageAllowSenders: []string{"a@b.com"}})

	// Nothing happens without a valid token.
	for name, body := range map[string]any{"none": nil, "empty": map[string]any{"confirm": ""}, "wrong": map[string]any{"confirm": "DELETE-AAAAAAAA"}} {
		if s, m := e.do(t, "DELETE", "/data", body); (name == "none" && s != 400 || name != "none" && s != 403) || e.count(t, `SELECT count(*) FROM messages`) != 2 {
			t.Fatalf("%s: %d %v", name, s, m)
		}
	}
	s, m := e.do(t, "POST", "/data:confirm", nil)
	tok, _ := m["token"].(string)
	if s != 200 || !strings.HasPrefix(tok, "DELETE-") || len(tok) != 15 || m["expires_in"] != float64(300) {
		t.Fatalf("confirm: %d %v", s, m)
	}
	// An expired token is refused.
	e.now = time.Now().UTC().Add(6 * time.Minute)
	if s, m := e.do(t, "DELETE", "/data", map[string]any{"confirm": tok}); s != 403 || m["code"] != "confirmation_invalid" {
		t.Fatalf("expired: %d %v", s, m)
	}
	e.now = time.Now().UTC()
	// A new request replaces the old token.
	_, m = e.do(t, "POST", "/data:confirm", nil)
	tok2 := m["token"].(string)
	if tok2 == tok {
		t.Fatal("token not renewed")
	}
	if s, _ := e.do(t, "DELETE", "/data", map[string]any{"confirm": tok}); s != 403 {
		t.Fatalf("old token accepted: %d", s)
	}
	s, m = e.do(t, "DELETE", "/data", map[string]any{"confirm": tok2})
	if s != 200 || m["deleted_messages"] != float64(2) || m["deleted_blobs"].(float64) < 6 {
		t.Fatalf("purge: %d %v", s, m)
	}
	// Everything stored is gone, including blobs no row owned.
	for _, k := range []string{m1.rawKey, m1.attKeys[0], m2.rawKey, m2.attKeys[0], orphan, gm} {
		if e.exists(t, k) {
			t.Fatalf("blob %s survived", k)
		}
	}
	for _, q := range []string{`messages`, `attachments`, `message_search`, `ingest_log`, `business_addresses`} {
		if n := e.count(t, `SELECT count(*) FROM `+q); n != 0 {
			t.Fatalf("%s has %d rows", q, n)
		}
	}
	if e.count(t, `SELECT count(*) FROM businesses WHERE last_message_at IS NOT NULL`) != 0 {
		t.Fatal("last_message_at not reset")
	}
	// Setup survives: business, rules, prefs, devices, unfinished imports.
	if e.count(t, `SELECT count(*) FROM businesses`) != 1 || e.count(t, `SELECT count(*) FROM rules`) != 1 ||
		e.count(t, `SELECT count(*) FROM devices`) != 1 || e.count(t, `SELECT count(*) FROM backfills WHERE status='paused'`) != 1 ||
		e.count(t, `SELECT count(*) FROM backfills WHERE status='done'`) != 0 {
		t.Fatal("purge deleted setup")
	}
	if _, p := e.do(t, "GET", "/prefs", nil); p["preview_lines"] != float64(3) {
		t.Fatalf("prefs lost: %v", p)
	}
	// The token is single use.
	if s, _ := e.do(t, "DELETE", "/data", map[string]any{"confirm": tok2}); s != 403 {
		t.Fatalf("token reused: %d", s)
	}
	// And the app still works afterwards.
	if s, m := e.do(t, "GET", "/messages", nil); s != 200 || len(m["messages"].([]any)) != 0 {
		t.Fatalf("%d %v", s, m)
	}
}

func TestPurgeWithoutBlobStore(t *testing.T) {
	e := setup(t)
	e.api.Blob = nil
	if s, m := e.do(t, "DELETE", "/data", map[string]any{"confirm": "x"}); s != 503 || m["code"] != "blob_unavailable" {
		t.Fatalf("%d %v", s, m)
	}
}

func TestRemovingABusinessPurgesItsMailAndBlobs(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	gone, kept := e.biz(t, "gone.com"), e.biz(t, "kept.com")
	g1 := e.ingestMail(t, "g1@x", "a@gone.com", day(1))
	g2 := e.ingestMail(t, "g2@x", "b@gone.com", day(2))
	k1 := e.ingestMail(t, "k1@x", "a@kept.com", day(1))
	// A backfilled copy and its blob, a rule, an import record and a queued write-back for the removed business.
	gmKey := "gmail/g3.eml"
	_ = e.blob.Put(ctx, gmKey, strings.NewReader("g3"), 2, nil)
	_, _ = e.db.Pool.Exec(ctx, `UPDATE messages SET raw_key=$2 WHERE id=$1`, g2.id, gmKey) // raw_key now points at the gmail copy
	_, _ = e.db.Pool.Exec(ctx, `INSERT INTO rules (id, name, position, business_id, gmail_label) VALUES ($1,'r',0,$2,'L')`, ids.NewV7(), gone.ID)
	_, _ = e.db.Pool.Exec(ctx, `INSERT INTO backfills (id, business_id, since, status) VALUES ($1,$2,now(),'done')`, ids.NewV7(), gone.ID)
	_ = e.db.Store.WantGmailArchived(ctx, []uuid.UUID{g1.id}, true, 0)

	if s, m := e.do(t, "DELETE", "/businesses/"+gone.ID.String(), nil); s != 204 {
		t.Fatalf("delete: %d %v", s, m)
	}
	for _, k := range []string{g1.rawKey, g1.attKeys[0], gmKey, g2.attKeys[0]} {
		if e.exists(t, k) {
			t.Fatalf("blob %s survived business removal", k)
		}
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM messages WHERE business_id IS NOT NULL AND business_id=` + "'" + gone.ID.String() + "'": 0,
		`SELECT count(*) FROM messages`:           1,
		`SELECT count(*) FROM attachments`:        1,
		`SELECT count(*) FROM message_search`:     1,
		`SELECT count(*) FROM gmail_sync`:         0,
		`SELECT count(*) FROM rules`:              0,
		`SELECT count(*) FROM backfills`:          0,
		`SELECT count(*) FROM business_addresses`: 1,
		`SELECT count(*) FROM businesses`:         1,
	} {
		if n := e.count(t, q); n != want {
			t.Errorf("%s = %d, want %d", q, n, want)
		}
	}
	if !e.exists(t, k1.rawKey) || !e.exists(t, k1.attKeys[0]) {
		t.Fatal("other business's blobs were removed")
	}
	_ = kept
}
