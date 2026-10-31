package health_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/crypto"
	"github.com/casprine/inbox/server/internal/events"
	"github.com/casprine/inbox/server/internal/gmail"
	"github.com/casprine/inbox/server/internal/health"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/store"
	"github.com/casprine/inbox/server/internal/testutil"
)

var now = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

type h struct {
	db *testutil.DB
	c  *health.Checker
}

func setup(t *testing.T) *h {
	t.Helper()
	db := testutil.NewDB(t)
	return &h{db: db, c: &health.Checker{Store: db.Store, Now: func() time.Time { return now }}}
}

func (x *h) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := x.db.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func (x *h) biz(t *testing.T, domain, routing string, last *time.Time, created time.Time) uuid.UUID {
	t.Helper()
	id := ids.NewV7()
	x.exec(t, `INSERT INTO businesses (id, name, domain, cf_zone_id, routing, last_message_at, created_at)
VALUES ($1,$2,$2,'z',$3::routing_state,$4,$5)`, id, domain, routing, last, created)
	return id
}

// msgs inserts n inbound edge messages for a business at the given times.
func (x *h) msgs(t *testing.T, biz uuid.UUID, times ...time.Time) {
	t.Helper()
	for _, at := range times {
		x.exec(t, `INSERT INTO messages (id, business_id, source, message_id_hdr, thread_key, received_at)
VALUES ($1,$2,'edge',$3,$3,$4)`, ids.NewV7(), biz, uuid.NewString(), at)
	}
}

func ago(d time.Duration) time.Time { return now.Add(-d) }

func (x *h) run(t *testing.T) (health.Report, map[string]health.Check) {
	t.Helper()
	r, err := x.c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]health.Check{}
	for _, c := range r.Checks {
		k := c.ID
		if c.BusinessID != nil {
			k += ":" + strings.TrimPrefix(c.Title, "Mail arriving · ")
		}
		by[k] = c
	}
	return r, by
}

func TestEmptyServerIsOK(t *testing.T) {
	x := setup(t)
	r, by := x.run(t)
	if r.State != "ok" || len(r.Checks) != 4 {
		t.Fatalf("%+v", r)
	}
	for id, want := range map[string]string{"worker": health.Off, "gmail": health.Off, "queue": health.OK, "alfather": health.Off} {
		if by[id].State != want {
			t.Errorf("%s: %+v", id, by[id])
		}
	}
	if !r.CheckedAt.Equal(now) {
		t.Fatal("checked_at")
	}
}

func TestMailArrivingPerBusiness(t *testing.T) {
	x := setup(t)
	good := ago(time.Hour)
	x.biz(t, "good.com", "ok", &good, ago(40*24*time.Hour))
	bad := x.biz(t, "email.com", "to_email", &good, ago(40*24*time.Hour))
	off := x.biz(t, "off.com", "none", &good, ago(40*24*time.Hour))
	old := ago(5 * 24 * time.Hour)
	x.biz(t, "quiet.com", "ok", &old, ago(40*24*time.Hour))
	x.biz(t, "fresh.com", "ok", nil, ago(2*24*time.Hour))
	x.biz(t, "neverarrived.com", "ok", nil, ago(10*24*time.Hour))
	x.biz(t, "unknown.com", "unknown", &good, ago(40*24*time.Hour))
	x.exec(t, `INSERT INTO businesses (id, name, domain, cf_zone_id, enabled) VALUES ($1,'disabled','disabled.com','z',false)`, ids.NewV7())

	r, by := x.run(t)
	if r.State != "needs_you" {
		t.Fatal(r.State)
	}
	if c := by["mail_arriving:good.com"]; c.State != health.OK || c.Message != "Mail is arriving" || c.FixRoute != nil {
		t.Errorf("good: %+v", c)
	}
	if c := by["mail_arriving:email.com"]; c.State != health.Error || !strings.Contains(c.Message, "sends straight to email again") ||
		c.FixRoute == nil || *c.FixRoute != "business/"+bad.String()+"/routing" {
		t.Errorf("to_email: %+v", c)
	}
	if c := by["mail_arriving:off.com"]; c.State != health.Error || c.FixRoute == nil || *c.FixRoute != "business/"+off.String()+"/routing" {
		t.Errorf("none: %+v", c)
	}
	if c := by["mail_arriving:quiet.com"]; c.State != health.Warn || c.Message != "No mail for 5 days" {
		t.Errorf("quiet: %+v", c)
	}
	if c := by["mail_arriving:fresh.com"]; c.State != health.OK || c.Message != "Waiting for the first message" {
		t.Errorf("fresh: %+v", c)
	}
	if c := by["mail_arriving:neverarrived.com"]; c.State != health.Warn {
		t.Errorf("never arrived after 10 days: %+v", c)
	}
	if c := by["mail_arriving:unknown.com"]; c.State != health.OK {
		t.Errorf("unknown routing must not complain: %+v", c)
	}
	if _, ok := by["mail_arriving:disabled.com"]; ok {
		t.Error("disabled business must not be checked")
	}
}

func TestQuietThresholdScalesWithMedianGap(t *testing.T) {
	x := setup(t)
	// A domain that gets one message every 5 days: 10 days of silence is normal, 16 is not.
	for _, tc := range []struct {
		domain string
		silent time.Duration
		want   string
	}{{"slow10.com", 10 * 24 * time.Hour, health.OK}, {"slow16.com", 16 * 24 * time.Hour, health.Warn}} {
		last := ago(tc.silent)
		id := x.biz(t, tc.domain, "ok", &last, ago(60*24*time.Hour))
		// Messages at -(silent+5d), -(silent+10d), -(silent+15d): median gap 5 days, within 30 days only if silent is small...
		x.msgs(t, id, ago(tc.silent), ago(tc.silent+5*24*time.Hour), ago(tc.silent+10*24*time.Hour))
	}
	// 30-day window: the 16-day case keeps 2 messages (gap 5d), the 10-day case 3.
	_, by := x.run(t)
	if c := by["mail_arriving:slow10.com"]; c.State != health.OK {
		t.Errorf("slow10: %+v", c)
	}
	if c := by["mail_arriving:slow16.com"]; c.State != health.Warn {
		t.Errorf("slow16: %+v", c)
	}
}

func TestWorkerCheck(t *testing.T) {
	x := setup(t)
	last := ago(time.Hour)
	b := x.biz(t, "a.com", "ok", &last, ago(40*24*time.Hour))
	// Hourly mail for the last 3 days, then it stops 30 hours ago.
	var times []time.Time
	for i := 30; i < 100; i++ {
		times = append(times, ago(time.Duration(i)*time.Hour))
	}
	x.msgs(t, b, times...)
	x.exec(t, `UPDATE businesses SET last_message_at=$2 WHERE id=$1`, b, ago(30*time.Hour))

	_, by := x.run(t)
	if c := by["worker"]; c.State != health.Warn || !strings.Contains(c.Message, "Nothing has come through for 30 hours") {
		t.Fatalf("silent worker: %+v", c)
	}
	// If routing is the problem, say that and not "worker silent".
	x.exec(t, `UPDATE businesses SET routing='to_email'`)
	if _, by := x.run(t); by["worker"].State != health.OK {
		t.Fatalf("routing broken should not blame the worker: %+v", by["worker"])
	}
	x.exec(t, `UPDATE businesses SET routing='ok'`)

	// Errors today win and are counted.
	x.exec(t, `INSERT INTO ingest_log (ingest_key, level, line, at) VALUES ('k','error','boom',$1), ('k','error','boom2',$1), ('k','info','fine',$1)`, ago(time.Hour))
	x.exec(t, `INSERT INTO ingest_log (ingest_key, level, line, at) VALUES ('k','error','old',$1)`, ago(48*time.Hour))
	if _, by := x.run(t); by["worker"].State != health.Warn || by["worker"].Message != "Running, 2 errors today" {
		t.Fatalf("errors: %+v", by["worker"])
	}
}

func TestQueueCheck(t *testing.T) {
	x := setup(t)
	ins := func(state string, scheduled time.Time, finalized *time.Time) {
		x.exec(t, `INSERT INTO river_job (state, kind, args, max_attempts, scheduled_at, finalized_at) VALUES ($1::river_job_state,'x','{}',5,$2,$3)`,
			state, scheduled, finalized)
	}
	fin := ago(time.Hour)
	ins("available", ago(time.Minute), nil)
	if _, by := x.run(t); by["queue"].State != health.OK {
		t.Fatalf("one fresh job: %+v", by["queue"])
	}
	ins("discarded", ago(2*time.Hour), &fin)
	if _, by := x.run(t); by["queue"].State != health.Warn || by["queue"].Message != "1 job failed in the last day" {
		t.Fatalf("discarded: %+v", by["queue"])
	}
	ins("available", ago(20*time.Minute), nil)
	if _, by := x.run(t); by["queue"].State != health.Error || !strings.HasPrefix(by["queue"].Message, "Stuck") {
		t.Fatalf("stuck: %+v", by["queue"])
	}
}

func TestGmailCheck(t *testing.T) {
	x := setup(t)
	ctx := context.Background()
	keys, _ := crypto.KeyringFromBase64("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	svc := &gmail.Service{Store: x.db.Store, Keys: keys, Cfg: gmail.Config{ClientID: "c", ClientSecret: "s"}}
	x.c.Gmail = svc
	_, by := x.run(t)
	if c := by["gmail"]; c.State != health.Off || c.FixRoute == nil || *c.FixRoute != "gmail" {
		t.Fatalf("not connected: %+v", c)
	}
	enc, _ := keys.Seal([]byte("r"), []byte("gmail_refresh_token"))
	acct, err := x.db.Store.UpsertGmailAccount(ctx, ids.NewV7(), "sam@gmail.com", enc, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if _, by := x.run(t); by["gmail"].State != health.OK || by["gmail"].Message != "Connected as sam@gmail.com" {
		t.Fatalf("connected: %+v", by["gmail"])
	}
	if err := x.db.Store.SetSetting(ctx, "gmail.rate_limited_until", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatal(err)
	}
	if _, by := x.run(t); by["gmail"].State != health.Warn || by["gmail"].Message != "Paused by Gmail rate limits" {
		t.Fatalf("rate limited: %+v", by["gmail"])
	}
	_ = x.db.Store.SetSetting(ctx, "gmail.rate_limited_until", time.Now().Add(-time.Hour).UTC())
	mid := ids.NewV7()
	x.exec(t, `INSERT INTO messages (id, source, message_id_hdr, thread_key, received_at) VALUES ($1,'edge','m','m',now())`, mid)
	x.exec(t, `INSERT INTO gmail_sync (message_id, state) VALUES ($1,'paused')`, mid)
	if _, err := x.db.Store.SetGmailAccountStatus(ctx, acct.ID, "needs_reauth"); err != nil {
		t.Fatal(err)
	}
	r, by := x.run(t)
	if c := by["gmail"]; c.State != health.Error || c.Message != "Signed out, 1 action waiting" || c.FixRoute == nil || *c.FixRoute != "gmail" {
		t.Fatalf("signed out: %+v", c)
	}
	if r.State != "needs_you" {
		t.Fatal(r.State)
	}
}

func TestAlfatherCheck(t *testing.T) {
	x := setup(t)
	id := ids.NewV7()
	x.exec(t, `INSERT INTO consumers (id, kind, token_hash, cursor_seq, last_seen_at) VALUES ($1,'alfather','\x01',0,$2)`, id, ago(time.Minute))
	x.exec(t, `INSERT INTO messages (id, source, message_id_hdr, thread_key, received_at) VALUES ($1,'edge','a','a',now()), ($2,'edge','b','b',now())`, ids.NewV7(), ids.NewV7())
	if _, by := x.run(t); by["alfather"].State != health.OK || by["alfather"].Message != "Connected, 2 waiting to be read" {
		t.Fatalf("connected: %+v", by["alfather"])
	}
	x.exec(t, `UPDATE consumers SET last_seen_at=$1`, ago(26*time.Hour))
	if _, by := x.run(t); by["alfather"].State != health.Warn || by["alfather"].Message != "Not reachable · last seen 26 hours ago" ||
		by["alfather"].FixRoute == nil || *by["alfather"].FixRoute != "alfather" {
		t.Fatalf("away: %+v", by["alfather"])
	}
}

type pub struct{ ev []events.Event }

func (p *pub) Publish(e events.Event) { p.ev = append(p.ev, e) }

func TestSnapshotStoresAndAnnouncesChangesOnly(t *testing.T) {
	x := setup(t)
	ctx := context.Background()
	p := &pub{}
	w := &health.SnapshotWorker{Checker: x.c, Events: p}
	work := func() {
		t.Helper()
		if err := w.Work(ctx, nil); err != nil {
			t.Fatal(err)
		}
	}
	work() // first snapshot: differs from "nothing", announced
	if len(p.ev) != 1 || p.ev[0].Type != events.TypeHealthChanged {
		t.Fatalf("first: %v", p.ev)
	}
	work() // identical
	if len(p.ev) != 1 {
		t.Fatalf("unchanged snapshot announced: %d", len(p.ev))
	}
	x.biz(t, "bad.com", "to_email", nil, ago(time.Hour))
	work()
	if len(p.ev) != 2 || p.ev[1].Fields["state"] != "needs_you" {
		t.Fatalf("change: %v", p.ev)
	}
	raw, err := x.db.Store.GetSetting(ctx, "health")
	if err != nil || !strings.Contains(string(raw), `"needs_you"`) {
		t.Fatalf("stored: %s %v", raw, err)
	}
	var _ store.Business
}
