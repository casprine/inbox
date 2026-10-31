package gmail

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/gmail/gmailtest"
	"github.com/casprine/inbox/server/internal/store"
)

type syncState struct {
	State    string
	Attempts int
	Err      *string
	Applied  string
	In       float64 // seconds until next attempt
}

func (e *env) syncState(t *testing.T, id uuid.UUID) syncState {
	t.Helper()
	var s syncState
	err := e.pool.QueryRow(context.Background(), `SELECT state::text, attempts, last_error, applied::text,
extract(epoch FROM next_attempt_at - now()) FROM gmail_sync WHERE message_id=$1`, id).Scan(&s.State, &s.Attempts, &s.Err, &s.Applied, &s.In)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) due(t *testing.T) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE gmail_sync SET next_attempt_at = now() WHERE state='pending'`); err != nil {
		t.Fatal(err)
	}
}

func (e *env) run(t *testing.T) int {
	t.Helper()
	n, err := e.eng.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	return n
}

// mailbox adds a message to the fake and the database with the same Message-ID.
func (e *env) mailbox(t *testing.T, name string) (uuid.UUID, *gmailtest.Msg) {
	t.Helper()
	m := e.fake.AddMessage(gmailtest.Msg{MessageID: name + "@x"})
	return e.insertMessage(t, "<"+name+"@x>"), m
}

func ptr(s string) *string { return &s }

func within(got, want, slack float64) bool { return got > want-slack && got <= want+slack }

func TestWritebackAppliesLabelArchiveRead(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.connect(t, "c1")
	id1, g1 := e.mailbox(t, "a")
	id2, g2 := e.mailbox(t, "b")
	for _, id := range []uuid.UUID{id1, id2} {
		if err := e.st.WantGmail(ctx, id, ptr("Acme/Invoices"), true, true, 0); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.run(t); n != 2 {
		t.Fatalf("processed %d", n)
	}
	for _, g := range []*gmailtest.Msg{g1, g2} {
		got := strings.Join(e.fake.LabelNames(g.ID), ",")
		if got != "Acme/Invoices" {
			t.Fatalf("labels in Gmail = %q, want only Acme/Invoices (INBOX and UNREAD removed)", got)
		}
	}
	if c := e.fake.Count("POST /gmail/v1/users/me/labels"); c != 1 {
		t.Fatalf("label created %d times, want once", c)
	}
	s := e.syncState(t, id1)
	if s.State != "synced" || s.Attempts != 0 || s.Err != nil || !strings.Contains(s.Applied, `"archived": true`) {
		t.Fatalf("state %+v", s)
	}
	var gid, tid *string
	_ = e.pool.QueryRow(ctx, `SELECT gmail_id, gmail_thread_id FROM messages WHERE id=$1`, id1).Scan(&gid, &tid)
	if gid == nil || *gid != g1.ID || tid == nil || *tid != g1.ThreadID {
		t.Fatalf("gmail ids not cached: %v %v", gid, tid)
	}
	// Nothing left to do.
	e.due(t)
	if n := e.run(t); n != 0 {
		t.Fatalf("second pass processed %d", n)
	}
	// Details expose the Gmail facts for the Message screen.
	status, m := e.do(t, "GET", "/messages/"+id1.String(), nil)
	if status != 200 {
		t.Fatalf("details status %d", status)
	}
	d := m["details"].(map[string]any)
	g := d["gmail"].(map[string]any)
	if d["gmail_id"] != g1.ID || g["state"] != "synced" || g["attempts"] != float64(0) || g["last_synced_at"] == nil || g["last_attempt_at"] == nil {
		t.Fatalf("details: %v", d)
	}
	if d["failure_reason"] != nil {
		t.Fatalf("failure_reason = %v", d["failure_reason"])
	}
	_, st := e.do(t, "GET", "/gmail/status", nil)
	if st["last_write_at"] == nil || st["waiting"] != float64(0) {
		t.Fatalf("status %v", st)
	}
}

func TestUndoInsideWindowMakesNoGmailCall(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.connect(t, "c1")
	id, g := e.mailbox(t, "a")
	if err := e.st.WantGmailArchived(ctx, []uuid.UUID{id}, true, 8*time.Second); err != nil {
		t.Fatal(err)
	}
	if n := e.run(t); n != 0 {
		t.Fatalf("row ran before the undo window ended (%d)", n)
	}
	if s := e.syncState(t, id); s.State != "pending" || !within(s.In, 8, 2) {
		t.Fatalf("scheduled wrong: %+v", s)
	}
	if err := e.st.WantGmailArchived(ctx, []uuid.UUID{id}, false, 8*time.Second); err != nil { // Undo
		t.Fatal(err)
	}
	e.due(t)
	e.run(t)
	if s := e.syncState(t, id); s.State != "synced" {
		t.Fatalf("state %+v", s)
	}
	if c := e.fake.Count("GET /gmail/v1/users/me/messages") + e.fake.Count("POST /gmail/v1/users/me/messages"); c != 0 {
		t.Fatalf("%d Gmail calls for an undone archive", c)
	}
	if !e.fake.Message(g.ID).Labels["INBOX"] {
		t.Fatal("INBOX lost")
	}

	// Archive for real, then unarchive later: INBOX comes back.
	_ = e.st.WantGmailArchived(ctx, []uuid.UUID{id}, true, 0)
	e.run(t)
	if e.fake.Message(g.ID).Labels["INBOX"] {
		t.Fatal("archive not applied")
	}
	_ = e.st.WantGmailArchived(ctx, []uuid.UUID{id}, false, 0)
	e.run(t)
	if !e.fake.Message(g.ID).Labels["INBOX"] {
		t.Fatal("unarchive not applied")
	}
}

func TestLabelChangeMovesLabel(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.connect(t, "c1")
	id, g := e.mailbox(t, "a")
	_ = e.st.WantGmail(ctx, id, ptr("A"), false, false, 0)
	e.run(t)
	_ = e.st.WantGmail(ctx, id, ptr("B"), false, false, 0)
	e.run(t)
	if got := strings.Join(e.fake.LabelNames(g.ID), ","); got != "B,INBOX,UNREAD" {
		t.Fatalf("labels %q", got)
	}
}

func TestLabelsOnlyIgnoresArchiveAndRead(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.connect(t, "c1")
	id, g := e.mailbox(t, "a")
	_ = e.st.WantGmail(ctx, id, ptr("Old"), true, true, 0)
	if _, err := e.pool.Exec(ctx, `UPDATE gmail_sync SET labels_only=true`); err != nil {
		t.Fatal(err)
	}
	e.run(t)
	if got := strings.Join(e.fake.LabelNames(g.ID), ","); got != "INBOX,Old,UNREAD" {
		t.Fatalf("labels %q", got)
	}
}

func TestNotFoundBackoffThenRetryEndpoint(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.connect(t, "c1")
	id := e.insertMessage(t, "<late@x>")
	_ = e.st.WantGmail(ctx, id, ptr("L"), false, false, 0)

	wantDelays := []float64{60, 60, 120, 240, 480}
	for i, d := range wantDelays {
		e.run(t)
		s := e.syncState(t, id)
		if s.State != "pending" || s.Attempts != i+1 || !within(s.In, d, 3) {
			t.Fatalf("after try %d: %+v want delay %v", i+1, s, d)
		}
		e.due(t)
	}
	e.run(t)
	if s := e.syncState(t, id); s.State != "not_found" || s.Attempts != 6 {
		t.Fatalf("after 6th try: %+v", s)
	}
	if n := e.run(t); n != 0 {
		t.Fatal("not_found rows must not be retried automatically")
	}
	// Message arrives in Gmail; the user taps Try again.
	e.fake.AddMessage(gmailtest.Msg{MessageID: "late@x"})
	status, m := e.do(t, "POST", "/messages/"+id.String()+"/gmail:retry", nil)
	if status != 202 || m["status"] != "queued" {
		t.Fatalf("retry: %d %v", status, m)
	}
	e.run(t)
	if s := e.syncState(t, id); s.State != "synced" || s.Attempts != 0 {
		t.Fatalf("after retry: %+v", s)
	}
	// Synced rows are not retryable.
	if status, m := e.do(t, "POST", "/messages/"+id.String()+"/gmail:retry", nil); status != 409 || m["code"] != "gmail_nothing_to_retry" {
		t.Fatalf("%d %v", status, m)
	}
	if status, m := e.do(t, "POST", "/messages/"+uuid.NewString()+"/gmail:retry", nil); status != 404 {
		t.Fatalf("unknown id: %d %v", status, m)
	}
}

func TestServerErrorRetriesThenSucceeds(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.connect(t, "c1")
	id, g := e.mailbox(t, "a")
	_ = e.st.WantGmail(ctx, id, nil, true, false, 0)
	e.fake.Fail("POST /gmail/v1/users/me/messages/", gmailtest.Failure{Status: 503})
	e.run(t)
	s := e.syncState(t, id)
	if s.State != "pending" || s.Attempts != 1 || s.Err == nil || !within(s.In, 60, 3) {
		t.Fatalf("after 503: %+v", s)
	}
	e.due(t)
	e.run(t)
	if s := e.syncState(t, id); s.State != "synced" || s.Attempts != 0 || s.Err != nil {
		t.Fatalf("after recovery: %+v", s)
	}
	if e.fake.Message(g.ID).Labels["INBOX"] {
		t.Fatal("not archived")
	}
}

func TestPersistentServerErrorsFailAfterSixTries(t *testing.T) {
	e := setup(t)
	e.connect(t, "c1")
	id, _ := e.mailbox(t, "a")
	_ = e.st.WantGmail(context.Background(), id, nil, true, false, 0)
	var fails []gmailtest.Failure
	for i := 0; i < 10; i++ {
		fails = append(fails, gmailtest.Failure{Status: 500})
	}
	e.fake.Fail("POST /gmail/v1/users/me/messages/", fails...)
	for i := 0; i < 6; i++ {
		e.due(t)
		e.run(t)
	}
	if s := e.syncState(t, id); s.State != "failed" || s.Attempts != 6 {
		t.Fatalf("%+v", s)
	}
}

func TestClientErrorFailsImmediatelyAndRetryWorks(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.connect(t, "c1")
	id, g := e.mailbox(t, "a")
	_ = e.st.WantGmail(ctx, id, nil, true, false, 0)
	e.fake.Fail("POST /gmail/v1/users/me/messages/", gmailtest.Failure{Status: 403, Body: `{"error":{"code":403,"message":"Insufficient Permission","errors":[{"reason":"insufficientPermissions"}]}}`})
	e.run(t)
	s := e.syncState(t, id)
	if s.State != "failed" || s.Err == nil || !strings.Contains(*s.Err, "Insufficient Permission") {
		t.Fatalf("%+v", s)
	}
	_, m := e.do(t, "GET", "/messages/"+id.String(), nil)
	if gm := m["details"].(map[string]any)["gmail"].(map[string]any); gm["state"] != "failed" || gm["last_error"] == nil {
		t.Fatalf("details gmail: %v", gm)
	}
	if status, _ := e.do(t, "POST", "/messages/"+id.String()+"/gmail:retry", nil); status != 202 {
		t.Fatal(status)
	}
	e.run(t)
	if s := e.syncState(t, id); s.State != "synced" || e.fake.Message(g.ID).Labels["INBOX"] {
		t.Fatalf("%+v", s)
	}
}

func TestRateLimitSnoozesEverything(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.connect(t, "c1")
	id1, _ := e.mailbox(t, "a")
	id2, g2 := e.mailbox(t, "b")
	for _, id := range []uuid.UUID{id1, id2} {
		_ = e.st.WantGmail(ctx, id, nil, true, false, 0)
	}
	e.fake.Fail("POST /gmail/v1/users/me/messages/", gmailtest.Failure{Status: 429, RetryAfter: "120"})
	e.eng.Concurrency = 1
	e.run(t)
	// The 429 pauses the whole queue: one row hit it, the other was not attempted past the pass.
	_, st := e.do(t, "GET", "/gmail/status", nil)
	if st["rate_limited_until"] == nil {
		t.Fatalf("status lacks rate_limited_until: %v", st)
	}
	until, _ := time.Parse(time.RFC3339Nano, st["rate_limited_until"].(string))
	if d := time.Until(until); d < 100*time.Second || d > 125*time.Second {
		t.Fatalf("rate limit window %v", d)
	}
	s1 := e.syncState(t, id1)
	if s1.State != "pending" || s1.Attempts != 0 || !within(s1.In, 120, 5) {
		t.Fatalf("limited row: %+v", s1)
	}
	e.due(t)
	if n := e.run(t); n != 0 {
		t.Fatalf("engine ran %d rows while rate limited", n)
	}
	if ev := e.ev.types(); ev[len(ev)-1] != "gmail.status" {
		t.Fatalf("no status event: %v", ev)
	}
	// Window passes: everything completes.
	if err := e.st.SetSetting(ctx, settingRateLimit, time.Now().Add(-time.Second).UTC()); err != nil {
		t.Fatal(err)
	}
	e.run(t)
	for _, id := range []uuid.UUID{id1, id2} {
		if s := e.syncState(t, id); s.State != "synced" {
			t.Fatalf("%v: %+v", id, s)
		}
	}
	if e.fake.Message(g2.ID).Labels["INBOX"] {
		t.Fatal("not archived")
	}
	_, st = e.do(t, "GET", "/gmail/status", nil)
	if st["rate_limited_until"] != nil {
		t.Fatalf("still limited: %v", st)
	}
}

func TestSignedOutPausesAndReconnectDrains(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.connect(t, "c1")
	id, g := e.mailbox(t, "a")
	_ = e.st.WantGmail(ctx, id, nil, true, false, 0)
	e.fake.RevokeGrant()
	e.svc.mu.Lock()
	e.svc.access = nil
	e.svc.mu.Unlock()
	e.run(t) // refresh -> invalid_grant -> needs_reauth, rows paused
	if s := e.syncState(t, id); s.State != "paused" {
		t.Fatalf("%+v", s)
	}
	if n := e.run(t); n != 0 {
		t.Fatal("engine must not run while signed out")
	}
	e.fake.RestoreGrant()
	e.connect(t, "c2")
	e.run(t)
	if s := e.syncState(t, id); s.State != "synced" || e.fake.Message(g.ID).Labels["INBOX"] {
		t.Fatalf("%+v", s)
	}
}

func TestNoAccountPausesQueue(t *testing.T) {
	e := setup(t)
	id := e.insertMessage(t, "<a@x>")
	_ = e.st.WantGmailArchived(context.Background(), []uuid.UUID{id}, true, 0)
	e.run(t)
	if s := e.syncState(t, id); s.State != "paused" {
		t.Fatalf("%+v", s)
	}
	if status, m := e.do(t, "POST", "/messages/"+id.String()+"/gmail:retry", nil); status != 409 || m["code"] != "gmail_not_connected" {
		t.Fatalf("%d %v", status, m)
	}
	_, st := e.do(t, "GET", "/gmail/status", nil)
	if st["waiting"] != float64(1) {
		t.Fatalf("%v", st)
	}
}

func TestStaleLabelIDIsReResolved(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.connect(t, "c1")
	id1, _ := e.mailbox(t, "a")
	_ = e.st.WantGmail(ctx, id1, ptr("Work"), false, false, 0)
	e.run(t)
	e.fake.DeleteLabel("Work") // user deleted it in Gmail; our cache still has the id
	id2, g2 := e.mailbox(t, "b")
	_ = e.st.WantGmail(ctx, id2, ptr("Work"), false, false, 0)
	e.run(t)
	if s := e.syncState(t, id2); s.State != "synced" {
		t.Fatalf("%+v", s)
	}
	if got := strings.Join(e.fake.LabelNames(g2.ID), ","); got != "INBOX,UNREAD,Work" {
		t.Fatalf("labels %q", got)
	}
}

func TestMessageDeletedInGmailGoesBackToLookup(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.connect(t, "c1")
	id, g := e.mailbox(t, "a")
	_ = e.st.WantGmail(ctx, id, nil, false, true, 0) // read
	e.run(t)
	// The message disappears; a new archive want arrives.
	e.fake.Fail("POST /gmail/v1/users/me/messages/", gmailtest.Failure{Status: 404})
	_ = e.st.WantGmailArchived(ctx, []uuid.UUID{id}, true, 0)
	e.run(t)
	s := e.syncState(t, id)
	if s.State != "pending" || s.Attempts != 1 {
		t.Fatalf("%+v", s)
	}
	var gid *string
	_ = e.pool.QueryRow(ctx, `SELECT gmail_id FROM messages WHERE id=$1`, id).Scan(&gid)
	if gid != nil {
		t.Fatalf("stale gmail_id kept: %s", *gid)
	}
	e.due(t)
	e.run(t) // lookup finds it again (the 404 was scripted once)
	if s := e.syncState(t, id); s.State != "synced" || e.fake.Message(g.ID).Labels["INBOX"] {
		t.Fatalf("%+v", s)
	}
}

func TestWantChangedWhileSyncingStaysPending(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id := e.insertMessage(t, "<a@x>")
	_ = e.st.WantGmailArchived(ctx, []uuid.UUID{id}, true, 0)
	rows, err := e.st.ClaimDueGmailSync(ctx, 10, time.Minute)
	if err != nil || len(rows) != 1 {
		t.Fatalf("claim: %v %d", err, len(rows))
	}
	// While the engine works on the archive, the user taps Undo.
	_ = e.st.WantGmailArchived(ctx, []uuid.UUID{id}, false, 8*time.Second)
	raw := []byte(`{"label":null,"archived":true,"read":false}`)
	js := rawMsg(raw)
	if err := e.st.FinishGmailSync(ctx, rows[0], store.SyncOutcome{State: "synced", Applied: &js}); err != nil {
		t.Fatal(err)
	}
	s := e.syncState(t, id)
	if s.State != "pending" || !strings.Contains(s.Applied, `"archived": true`) {
		t.Fatalf("undo during sync was lost: %+v", s)
	}
	// Claimed rows are leased: a second claim gets nothing.
	_ = e.st.WantGmailArchived(ctx, []uuid.UUID{id}, true, 0)
	if r, _ := e.st.ClaimDueGmailSync(ctx, 10, time.Minute); len(r) != 1 {
		t.Fatalf("claim = %d", len(r))
	}
	if r, _ := e.st.ClaimDueGmailSync(ctx, 10, time.Minute); len(r) != 0 {
		t.Fatalf("leased row claimed twice: %d", len(r))
	}
}

func rawMsg(b []byte) json.RawMessage { return json.RawMessage(b) }
