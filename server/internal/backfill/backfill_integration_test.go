package backfill_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/casprine/inbox/server/internal/auth"
	"github.com/casprine/inbox/server/internal/backfill"
	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/crypto"
	"github.com/casprine/inbox/server/internal/events"
	"github.com/casprine/inbox/server/internal/gmail"
	"github.com/casprine/inbox/server/internal/gmail/gmailtest"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest"
	"github.com/casprine/inbox/server/internal/mail"
	"github.com/casprine/inbox/server/internal/store"
	"github.com/casprine/inbox/server/internal/testutil"
)

var today = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

type queue struct {
	mu  sync.Mutex
	ids []uuid.UUID
}

func (q *queue) EnqueueBackfill(_ context.Context, id uuid.UUID) error {
	q.mu.Lock()
	q.ids = append(q.ids, id)
	q.mu.Unlock()
	return nil
}
func (q *queue) take() []uuid.UUID {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.ids
	q.ids = nil
	return out
}

type pub struct {
	mu sync.Mutex
	ev []events.Event
	on func(events.Event)
}

func (p *pub) Publish(e events.Event) {
	p.mu.Lock()
	p.ev = append(p.ev, e)
	on := p.on
	p.mu.Unlock()
	if on != nil {
		on(e)
	}
}

type env struct {
	db     *testutil.DB
	fake   *gmailtest.Fake
	gsvc   *gmail.Service
	svc    *backfill.Service
	worker *backfill.Worker
	engine *gmail.Engine
	q      *queue
	ev     *pub
	blob   *blob.Store
	srv    *httptest.Server
}

func setup(t *testing.T) *env {
	t.Helper()
	ep := os.Getenv("TEST_S3_ENDPOINT")
	if ep == "" {
		t.Skip("TEST_S3_ENDPOINT not set")
	}
	db := testutil.NewDB(t)
	fake := gmailtest.New()
	t.Cleanup(fake.Close)
	keys, _ := crypto.KeyringFromBase64("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	gsvc := &gmail.Service{Store: db.Store, Keys: keys, Cfg: gmail.Config{ClientID: "c", ClientSecret: "s",
		AuthURL: fake.AuthURL(), TokenURL: fake.TokenURL(), RevokeURL: fake.RevokeURL(), APIBase: fake.APIBase()}}
	bl := blob.New(blob.Config{Endpoint: ep, Bucket: "inbox-mail", AccessKey: "minio", SecretKey: "minio12345"})
	ing := &ingest.Worker{Store: db.Store, Blob: bl, Router: ingest.RulesRouter{}}
	q := &queue{}
	ev := &pub{}
	svc := &backfill.Service{Store: db.Store, Gmail: gsvc, Queue: q, Events: ev}
	w := &backfill.Worker{Gmail: gsvc, Store: db.Store, Blob: bl, Ingest: ing, Events: ev, PageSize: 2}
	eng := &gmail.Engine{Svc: gsvc, Store: db.Store, Jitter: func(d time.Duration) time.Duration { return d }}
	api := &backfill.API{Svc: svc, Now: func() time.Time { return today }}
	mapi := &mail.API{Store: db.Store, Fetch: &backfill.Importer{Gmail: gsvc, Store: db.Store, Blob: bl, Ingest: ing}}
	asvc := &auth.Service{Store: db.Store, RateLimit: 1000, RateBurst: 1000}
	r := chi.NewRouter()
	asvc.Routes(r, func(r chi.Router) { api.Routes(r); mapi.Routes(r) })
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &env{db: db, fake: fake, gsvc: gsvc, svc: svc, worker: w, engine: eng, q: q, ev: ev, blob: bl, srv: srv}
}

func (e *env) connect(t *testing.T) {
	t.Helper()
	enc, _ := e.gsvc.Keys.Seal([]byte("refresh-1"), []byte("gmail_refresh_token"))
	if _, err := e.db.Store.UpsertGmailAccount(context.Background(), ids.NewV7(), "sam@gmail.com", enc, "k1"); err != nil {
		t.Fatal(err)
	}
}

func (e *env) biz(t *testing.T, domain string) store.Business {
	t.Helper()
	b, err := e.db.Store.CreateBusiness(context.Background(), store.Business{ID: ids.NewV7(), Name: domain, Domain: domain, CFZoneID: "z", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (e *env) do(t *testing.T, method, path string, body any) (int, map[string]any) {
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

func eml(msgID, from, to, subject string) []byte {
	return []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nMessage-ID: <%s>\r\nSubject: %s\r\nDate: Mon, 02 Mar 2026 10:00:00 +0000\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nbody of %s\r\n",
		from, to, msgID, subject, subject))
}

// addMail puts a message into the fake mailbox. to is both the To header and the searchable recipient.
func (e *env) addMail(msgID, to, subject string, age time.Duration, labels ...string) *gmailtest.Msg {
	l := map[string]bool{"INBOX": true, "UNREAD": true}
	for _, x := range labels {
		l[x] = true
	}
	return e.fake.AddMessage(gmailtest.Msg{MessageID: msgID, To: []string{strings.ToLower(to)}, Labels: l,
		Date: today.Add(-age), Raw: eml(msgID, "Pat <pat@client.com>", to, subject)})
}

func (e *env) job(attempt int) *river.Job[backfill.Args] {
	return &river.Job[backfill.Args]{JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: 10}}
}

func (e *env) work(t *testing.T, id uuid.UUID) error {
	t.Helper()
	j := e.job(1)
	j.Args = backfill.Args{BackfillID: id}
	return e.worker.Work(context.Background(), j)
}

func (e *env) get(t *testing.T, id uuid.UUID) store.Backfill {
	t.Helper()
	b, err := e.db.Store.GetBackfill(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func day(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

func TestEstimate(t *testing.T) {
	e := setup(t)
	acme, beta := e.biz(t, "acme.com"), e.biz(t, "beta.com")
	for i := 0; i < 5; i++ {
		e.addMail(fmt.Sprintf("a%d@x", i), "hello@acme.com", "A", day(i+1))
	}
	e.addMail("old@x", "hello@acme.com", "old", day(200))         // before the window
	e.addMail("sent@x", "hello@acme.com", "mine", day(2), "SENT") // your own mail
	for i := 0; i < 3; i++ {
		e.addMail(fmt.Sprintf("b%d@x", i), "info@beta.com", "B", day(i+1))
	}
	e.connect(t)

	s, m := e.do(t, "GET", "/backfills:estimate?days=90", nil)
	if s != 200 || m["total"] != float64(8) || m["since"] != "2025-12-10" || m["eta_seconds"] != float64(0) {
		t.Fatalf("%d %v", s, m)
	}
	got := map[string]float64{}
	for _, b := range m["businesses"].([]any) {
		b := b.(map[string]any)
		got[b["domain"].(string)] = b["estimate"].(float64)
	}
	if got["acme.com"] != 5 || got["beta.com"] != 3 {
		t.Fatalf("%v", got)
	}
	_, m = e.do(t, "GET", "/backfills:estimate?since=2020-01-01&businesses="+acme.ID.String(), nil)
	if m["total"] != float64(6) || len(m["businesses"].([]any)) != 1 { // includes the 200-day-old one, not the sent one
		t.Fatalf("%v", m)
	}
	_ = beta
	for _, bad := range []string{"", "?days=0", "?days=99999", "?days=x", "?since=2030-01-01", "?since=nope", "?days=5&since=2026-01-01"} {
		if s, m := e.do(t, "GET", "/backfills:estimate"+bad, nil); s != 400 || m["code"] != "invalid_range" {
			t.Fatalf("%q: %d %v", bad, s, m)
		}
	}
	if s, m := e.do(t, "GET", "/backfills:estimate?days=5&businesses=zzz", nil); s != 400 || m["code"] != "invalid_business" {
		t.Fatalf("%d %v", s, m)
	}
	if s, _ := e.do(t, "GET", "/backfills:estimate?days=5&businesses="+uuid.NewString(), nil); s != 404 {
		t.Fatalf("unknown business: %d", s)
	}
}

func TestBackfillImportsDedupesAndWritesLabelsOnly(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	acme := e.biz(t, "acme.com")
	e.connect(t)
	// A rule that labels (and would archive, were it not label-only for backfills).
	lbl := "Acme/Invoices"
	if _, err := e.db.Store.CreateRuleTop(ctx, newRule(acme.ID, "invoices", &lbl)); err != nil {
		t.Fatal(err)
	}

	m1 := e.addMail("m1@x", "invoices@acme.com", "Invoice 1", day(1)) // routed, unread
	m2 := e.addMail("m2@x", "hello@acme.com", "Hi", day(2))           // unrouted, read
	delete(m2.Labels, "UNREAD")
	m3 := e.fake.AddMessage(gmailtest.Msg{MessageID: "m3@x", To: []string{"hello@acme.com"}, Date: today.Add(-day(3)), // addressed elsewhere in the headers
		Raw: eml("m3@x", "pat@client.com", "someone@elsewhere.org", "Not ours")})
	m4 := e.addMail("m4@x", "invoices@acme.com", "Already here", day(4)) // received live earlier
	e.addMail("sent@x", "invoices@acme.com", "mine", day(2), "SENT")     // excluded by the query
	e.addMail("old@x", "invoices@acme.com", "old", day(300))             // before the window
	e.addMail("m5@x", "invoices@acme.com", "Invoice 5", day(5))
	e.addMail("m6@x", "other@acme.com", "Six", day(6))
	_ = m3

	// m4 was ingested by the edge path earlier.
	if _, err := e.worker.Ingest.Ingest(ctx, mustParse(t, m4.Raw), ingest.RawOpts{Key: "raw/live.eml", EnvTo: "invoices@acme.com",
		ReceivedAt: today.Add(-day(4)), Size: len(m4.Raw), Source: "edge"}); err != nil {
		t.Fatal(err)
	}

	s, m := e.do(t, "POST", "/backfills", map[string]any{"days": 90})
	if s != 201 {
		t.Fatalf("create: %d %v", s, m)
	}
	bfs := m["backfills"].([]any)
	if len(bfs) != 1 || bfs[0].(map[string]any)["status"] != "queued" || bfs[0].(map[string]any)["estimated"] != float64(6) || bfs[0].(map[string]any)["since"] != "2025-12-10" {
		t.Fatalf("created: %v", bfs)
	}
	id := uuid.MustParse(bfs[0].(map[string]any)["id"].(string))
	if q := e.q.take(); len(q) != 1 || q[0] != id {
		t.Fatalf("enqueued %v", q)
	}

	if err := e.work(t, id); err != nil {
		t.Fatal(err)
	}
	b := e.get(t, id)
	// candidates: m1 m2 m3 m4 m5 m6 -> imported m1 m2 m5 m6, skipped m3 (no business) and m4 (duplicate)
	if b.Status != "done" || b.Scanned != 6 || b.Imported != 4 || b.Sorted != 2 || b.Unrouted != 2 || b.Skipped != 2 || b.FinishedAt == nil || b.PageToken != nil {
		t.Fatalf("result: %+v", b)
	}

	row := func(hdr string) (src string, read bool, gid *string, route string, biz *uuid.UUID) {
		err := e.db.Pool.QueryRow(ctx, `SELECT source::text, is_read, gmail_id, route::text, business_id FROM messages WHERE message_id_hdr=$1`, hdr).Scan(&src, &read, &gid, &route, &biz)
		if err != nil {
			t.Fatalf("%s: %v", hdr, err)
		}
		return
	}
	src, read, gid, route, biz := row("m1@x")
	if src != "backfill" || read || gid == nil || *gid != m1.ID || route != "routed" || biz == nil || *biz != acme.ID {
		t.Fatalf("m1: %s %v %v %s %v", src, read, gid, route, biz)
	}
	if _, read, _, route, _ := row("m2@x"); !read || route != "unrouted" {
		t.Fatalf("m2: read=%v route=%s", read, route)
	}
	var n int
	_ = e.db.Pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE message_id_hdr IN ('m3@x','sent@x','old@x')`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d messages that must not be imported", n)
	}
	// The live copy learned its Gmail id and was not duplicated.
	_ = e.db.Pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE message_id_hdr='m4@x'`).Scan(&n)
	var g4 *string
	_ = e.db.Pool.QueryRow(ctx, `SELECT gmail_id FROM messages WHERE message_id_hdr='m4@x'`).Scan(&g4)
	if n != 1 || g4 == nil || *g4 != m4.ID {
		t.Fatalf("m4: count=%d gmail_id=%v", n, g4)
	}
	// Raw copies are kept for the Original view.
	var rk string
	_ = e.db.Pool.QueryRow(ctx, `SELECT raw_key FROM messages WHERE message_id_hdr='m1@x'`).Scan(&rk)
	if rk != "gmail/"+m1.ID+".eml" {
		t.Fatal(rk)
	}
	if _, err := e.blob.Get(ctx, rk); err != nil {
		t.Fatal(err)
	}
	var skippedBlob int
	_ = e.db.Pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE raw_key LIKE 'gmail/%'`).Scan(&skippedBlob)
	if skippedBlob != 4 {
		t.Fatalf("raw keys %d", skippedBlob)
	}

	// Write-back: label only, never archive or mark read, even though the rule asks for it.
	var labelsOnly bool
	_ = e.db.Pool.QueryRow(ctx, `SELECT gs.labels_only FROM gmail_sync gs JOIN messages m ON m.id=gs.message_id WHERE m.message_id_hdr='m1@x'`).Scan(&labelsOnly)
	if !labelsOnly {
		t.Fatal("backfill write-back must be labels-only")
	}
	if _, err := e.db.Pool.Exec(ctx, `UPDATE gmail_sync SET next_attempt_at=now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.engine.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.fake.LabelNames(m1.ID), ","); got != "Acme/Invoices,INBOX,UNREAD" {
		t.Fatalf("Gmail labels after write-back: %q", got)
	}
	if e.fake.Count("GET /gmail/v1/users/me/messages?q=rfc822msgid") != 0 {
		t.Fatal("known Gmail ids must skip the lookup")
	}

	// Progress was announced and ended with done.
	last := e.ev.ev[len(e.ev.ev)-1]
	if last.Type != events.TypeJobProgress || last.Fields["status"] != "done" || last.Fields["imported"] != 4 || last.Fields["job"] != "backfill" {
		t.Fatalf("last event: %v %v", last.Type, last.Fields)
	}

	// Re-running the range is safe: everything is a duplicate now.
	_, m = e.do(t, "POST", "/backfills", map[string]any{"since": "2025-12-10"})
	id2 := uuid.MustParse(m["backfills"].([]any)[0].(map[string]any)["id"].(string))
	if err := e.work(t, id2); err != nil {
		t.Fatal(err)
	}
	if b := e.get(t, id2); b.Status != "done" || b.Imported != 0 || b.Skipped != 6 {
		t.Fatalf("rerun: %+v", b)
	}
	_, list := e.do(t, "GET", "/backfills", nil)
	if l := list["backfills"].([]any); len(l) != 2 || l[0].(map[string]any)["id"] != id2.String() {
		t.Fatalf("list newest first: %v", l)
	}
}

func TestRateLimitPausesAndResumesExactly(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	acme := e.biz(t, "acme.com")
	e.connect(t)
	for i := 0; i < 5; i++ {
		e.addMail(fmt.Sprintf("r%d@x", i), "hello@acme.com", fmt.Sprintf("S%d", i), day(i+1))
	}
	id := ids.NewV7()
	if err := e.db.Store.CreateBackfill(ctx, id, acme.ID, today.Add(-day(90)), 5); err != nil {
		t.Fatal(err)
	}
	// Third raw download is rate limited.
	e.fake.Fail("GET /gmail/v1/users/me/messages/", gmailtest.Failure{Status: 429, RetryAfter: "30", Skip: 2})

	err := e.work(t, id)
	var sn *river.JobSnoozeError
	if !errors.As(err, &sn) || sn.Duration < 30*time.Second || sn.Duration > 40*time.Second {
		t.Fatalf("want snooze ~30s, got %v", err)
	}
	b := e.get(t, id)
	if b.Status != "paused" || b.PausedUntil == nil || time.Until(*b.PausedUntil) < 20*time.Second || b.Scanned != 2 || b.Imported != 2 {
		t.Fatalf("paused state: %+v", b)
	}
	// Still inside the window: the job snoozes again and touches nothing.
	if err := e.work(t, id); !errors.As(err, &sn) {
		t.Fatalf("early run: %v", err)
	}
	if e.get(t, id).Imported != 2 {
		t.Fatal("work during the rate-limit window")
	}
	_, list := e.do(t, "GET", "/backfills", nil)
	if bj := list["backfills"].([]any)[0].(map[string]any); bj["status"] != "paused" || bj["pause_reason"] != "rate_limited" || bj["paused_until"] == nil {
		t.Fatalf("api: %v", bj)
	}

	// Window over.
	if _, err := e.db.Pool.Exec(ctx, `UPDATE backfills SET paused_until = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if err := e.work(t, id); err != nil {
		t.Fatal(err)
	}
	b = e.get(t, id)
	if b.Status != "done" || b.Scanned != 5 || b.Imported != 5 || b.Skipped != 0 || b.LastError != nil || b.PausedUntil != nil {
		t.Fatalf("after resume: %+v", b)
	}
}

func TestPauseResumeCancelControls(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.biz(t, "acme.com")
	e.connect(t)
	for i := 0; i < 6; i++ {
		e.addMail(fmt.Sprintf("p%d@x", i), "hello@acme.com", fmt.Sprintf("S%d", i), day(i+1))
	}
	_, m := e.do(t, "POST", "/backfills", map[string]any{"days": 30})
	id := uuid.MustParse(m["backfills"].([]any)[0].(map[string]any)["id"].(string))
	e.q.take()

	// A second backfill for the same business is refused while this one is unfinished.
	if s, m := e.do(t, "POST", "/backfills", map[string]any{"days": 30}); s != 409 || m["code"] != "backfill_active" {
		t.Fatalf("%d %v", s, m)
	}

	// Pause from the app after the first page: the worker stops at the next page boundary.
	e.ev.on = func(ev events.Event) {
		if ev.Fields["scanned"] == 2 && ev.Fields["status"] == "running" {
			if _, err := e.svc.Pause(ctx, id); err != nil {
				t.Errorf("pause: %v", err)
			}
		}
	}
	if err := e.work(t, id); err != nil {
		t.Fatal(err)
	}
	e.ev.on = nil
	b := e.get(t, id)
	if b.Status != "paused" || b.Scanned != 2 || b.PausedUntil != nil || b.LastError != nil {
		t.Fatalf("after pause: %+v", b)
	}
	_, list := e.do(t, "GET", "/backfills", nil)
	if list["backfills"].([]any)[0].(map[string]any)["pause_reason"] != "user" {
		t.Fatalf("%v", list)
	}
	// A paused job that runs anyway does nothing.
	if err := e.work(t, id); err != nil || e.get(t, id).Scanned != 2 {
		t.Fatal("paused backfill ran")
	}
	// Pausing twice is a conflict.
	if s, m := e.do(t, "POST", "/backfills/"+id.String()+":pause", nil); s != 409 || m["code"] != "backfill_bad_state" {
		t.Fatalf("%d %v", s, m)
	}

	s, m := e.do(t, "POST", "/backfills/"+id.String()+":resume", nil)
	if s != 200 || m["status"] != "queued" {
		t.Fatalf("resume: %d %v", s, m)
	}
	if q := e.q.take(); len(q) != 1 || q[0] != id {
		t.Fatalf("resume must enqueue: %v", q)
	}
	if err := e.work(t, id); err != nil {
		t.Fatal(err)
	}
	if b := e.get(t, id); b.Status != "done" || b.Scanned != 6 || b.Imported != 6 {
		t.Fatalf("finished: %+v", b)
	}
	// Done backfills cannot be paused, resumed or cancelled.
	for _, op := range []string{"pause", "resume", "cancel"} {
		if s, m := e.do(t, "POST", "/backfills/"+id.String()+":"+op, nil); s != 409 || m["code"] != "backfill_bad_state" {
			t.Fatalf("%s on done: %d %v", op, s, m)
		}
	}
	if s, _ := e.do(t, "POST", "/backfills/"+uuid.NewString()+":cancel", nil); s != 404 {
		t.Fatalf("unknown: %d", s)
	}

	// Cancel keeps what was imported and stops further work.
	e.addMail("late@x", "hello@acme.com", "late", day(1))
	_, m = e.do(t, "POST", "/backfills", map[string]any{"days": 30})
	id2 := uuid.MustParse(m["backfills"].([]any)[0].(map[string]any)["id"].(string))
	if s, m := e.do(t, "POST", "/backfills/"+id2.String()+":cancel", nil); s != 200 || m["status"] != "cancelled" || m["finished_at"] == nil {
		t.Fatalf("cancel: %d %v", s, m)
	}
	if err := e.work(t, id2); err != nil || e.get(t, id2).Scanned != 0 {
		t.Fatal("cancelled backfill ran")
	}
}

func TestSignedOutPausesUntilReconnect(t *testing.T) {
	e := setup(t) // no Gmail account
	ctx := context.Background()
	acme := e.biz(t, "acme.com")
	id := ids.NewV7()
	if err := e.db.Store.CreateBackfill(ctx, id, acme.ID, today.Add(-day(30)), 0); err != nil {
		t.Fatal(err)
	}
	if err := e.work(t, id); err != nil {
		t.Fatal(err)
	}
	if b := e.get(t, id); b.Status != "paused" || b.LastError == nil || *b.LastError != "gmail_signed_out" || b.PausedUntil != nil {
		t.Fatalf("%+v", b)
	}
	_, list := e.do(t, "GET", "/backfills", nil)
	if list["backfills"].([]any)[0].(map[string]any)["pause_reason"] != "gmail_signed_out" {
		t.Fatalf("%v", list)
	}
	// Reconnecting requeues it.
	e.connect(t)
	e.svc.ResumeSignedOut(ctx)
	if q := e.q.take(); len(q) != 1 || q[0] != id {
		t.Fatalf("requeued %v", q)
	}
	if err := e.work(t, id); err != nil {
		t.Fatal(err)
	}
	if b := e.get(t, id); b.Status != "done" {
		t.Fatalf("%+v", b)
	}
}

func TestCreateRequiresGmail(t *testing.T) {
	e := setup(t)
	e.biz(t, "acme.com")
	if s, m := e.do(t, "POST", "/backfills", map[string]any{"days": 30}); s != 409 || m["code"] != "gmail_not_connected" {
		t.Fatalf("%d %v", s, m)
	}
	if s, m := e.do(t, "GET", "/backfills:estimate?days=30", nil); s != 409 || m["code"] != "gmail_not_connected" {
		t.Fatalf("%d %v", s, m)
	}
}

func TestTransientErrorsRetryThenFail(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	acme := e.biz(t, "acme.com")
	e.connect(t)
	e.addMail("t1@x", "hello@acme.com", "S", day(1))
	id := ids.NewV7()
	_ = e.db.Store.CreateBackfill(ctx, id, acme.ID, today.Add(-day(30)), 1)
	e.fake.Fail("GET /gmail/v1/users/me/messages", gmailtest.Failure{Status: 500}, gmailtest.Failure{Status: 500})

	j := e.job(1)
	j.Args = backfill.Args{BackfillID: id}
	if err := e.worker.Work(ctx, j); err == nil {
		t.Fatal("first failure must be returned so River retries")
	}
	if b := e.get(t, id); b.Status != "running" {
		t.Fatalf("still running between retries: %+v", b)
	}
	j = e.job(10) // last attempt
	j.Args = backfill.Args{BackfillID: id}
	if err := e.worker.Work(ctx, j); err != nil {
		t.Fatalf("final failure is recorded, not returned: %v", err)
	}
	b := e.get(t, id)
	if b.Status != "failed" || b.LastError == nil || b.FinishedAt == nil {
		t.Fatalf("%+v", b)
	}
	_, list := e.do(t, "GET", "/backfills", nil)
	if bj := list["backfills"].([]any)[0].(map[string]any); bj["status"] != "failed" || bj["last_error"] == nil {
		t.Fatalf("%v", bj)
	}
}

func TestCreateWithoutQueue(t *testing.T) {
	e := setup(t)
	e.biz(t, "acme.com")
	e.connect(t)
	e.svc.Queue = nil
	if s, m := e.do(t, "POST", "/backfills", map[string]any{"days": 30}); s != 503 || m["code"] != "queue_unavailable" {
		t.Fatalf("%d %v", s, m)
	}
	if s, m := e.do(t, "POST", "/backfills", map[string]any{"days": 30, "extra": 1}); s != 400 {
		t.Fatalf("%d %v", s, m)
	}
}

func TestRetryFailedViaGmail(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	acme := e.biz(t, "acme.com")
	lbl := "Acme/Invoices"
	if _, err := e.db.Store.CreateRuleTop(ctx, newRule(acme.ID, "invoices", &lbl)); err != nil {
		t.Fatal(err)
	}
	failedRow := func(msgID string) uuid.UUID {
		t.Helper()
		err := e.worker.Ingest.Work(ctx, &river.Job[ingest.IngestArgs]{Args: ingest.IngestArgs{
			FailID: uuid.NewString(), Reason: "store_failed", EnvFrom: "pat@client.com", EnvTo: "invoices@acme.com",
			ReceivedAt: today.Format(time.RFC3339), MessageID: "<" + msgID + ">"}})
		if err != nil {
			t.Fatal(err)
		}
		var id uuid.UUID
		if err := e.db.Pool.QueryRow(ctx, `SELECT id FROM messages WHERE state='failed' ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f1 := failedRow("lost1@x")

	// Gmail not connected.
	if s, m := e.do(t, "POST", "/failed/"+f1.String()+":retry", nil); s != 409 || m["code"] != "needs_gmail" {
		t.Fatalf("no gmail: %d %v", s, m)
	}
	e.connect(t)
	// Gmail does not have it.
	if s, m := e.do(t, "POST", "/failed/"+f1.String()+":retry", nil); s != 409 || m["code"] != "not_in_gmail" {
		t.Fatalf("not in gmail: %d %v", s, m)
	}
	var still int
	_ = e.db.Pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE id=$1 AND state='failed'`, f1).Scan(&still)
	if still != 1 {
		t.Fatal("failed row must survive an unsuccessful retry")
	}

	g := e.addMail("lost1@x", "invoices@acme.com", "Lost invoice", day(1))
	s, m := e.do(t, "POST", "/failed/"+f1.String()+":retry", nil)
	if s != 200 || m["status"] != "imported" || m["id"] == nil {
		t.Fatalf("retry: %d %v", s, m)
	}
	var state, route, src string
	var gid, rawKey *string
	var biz *uuid.UUID
	err := e.db.Pool.QueryRow(ctx, `SELECT state::text, route::text, source::text, gmail_id, raw_key, business_id FROM messages WHERE id=$1`, m["id"]).
		Scan(&state, &route, &src, &gid, &rawKey, &biz)
	if err != nil || state != "stored" || route != "routed" || src != "edge" || gid == nil || *gid != g.ID || rawKey == nil || *rawKey != "gmail/"+g.ID+".eml" || biz == nil || *biz != acme.ID {
		t.Fatalf("imported row: %v %s %s %s %v %v %v", err, state, route, src, gid, rawKey, biz)
	}
	_ = e.db.Pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE id=$1`, f1).Scan(&still)
	if still != 0 {
		t.Fatal("failed row must be gone after a successful retry")
	}
	// Attention counts no longer include it.
	_, att := e.do(t, "GET", "/attention", nil)
	if att["failed"].(map[string]any)["count"] != float64(0) {
		t.Fatalf("attention: %v", att)
	}
}
