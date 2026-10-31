package gmail

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/gmail/gmailtest"
	"github.com/casprine/inbox/server/internal/ingest/parse"
	"github.com/casprine/inbox/server/internal/store"

	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest"
)

func (e *env) business(t *testing.T, domain string) store.Business {
	t.Helper()
	b, err := e.st.CreateBusiness(context.Background(), store.Business{ID: ids.NewV7(), Name: domain, Domain: domain, CFZoneID: "z", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func labelNames(m map[string]any) []string {
	var out []string
	for _, l := range m["labels"].([]any) {
		out = append(out, l.(map[string]any)["name"].(string))
	}
	return out
}

func TestLabelsEndpoints(t *testing.T) {
	e := setup(t)
	if s, m := e.do(t, "GET", "/gmail/labels", nil); s != 409 || m["code"] != "gmail_not_connected" {
		t.Fatalf("not connected: %d %v", s, m)
	}
	e.connect(t, "c1")
	e.fake.AddLabel("Zeta")
	e.fake.AddLabel("Acme/Invoices")

	s, m := e.do(t, "GET", "/gmail/labels", nil)
	if got := strings.Join(labelNames(m), ","); s != 200 || got != "Acme/Invoices,Zeta" {
		t.Fatalf("list: %d %q (system labels must be hidden)", s, got)
	}
	if id := m["labels"].([]any)[1].(map[string]any)["id"]; id != e.fake.LabelID("Zeta") {
		t.Fatalf("id %v", id)
	}

	s, m = e.do(t, "POST", "/gmail/labels", map[string]any{"name": "  Acme/Receipts "})
	if s != 201 || m["name"] != "Acme/Receipts" || m["created"] != true || m["id"] != e.fake.LabelID("Acme/Receipts") {
		t.Fatalf("create: %d %v", s, m)
	}
	s, m = e.do(t, "POST", "/gmail/labels", map[string]any{"name": "Acme/Receipts"})
	if s != 200 || m["created"] != false {
		t.Fatalf("existing: %d %v", s, m)
	}
	if n := e.fake.Count("POST /gmail/v1/users/me/labels"); n != 1 {
		t.Fatalf("label created %d times", n)
	}
	for _, bad := range []string{"", "   ", "/x", "x/", "a//b", "INBOX", "inbox", strings.Repeat("x", 226)} {
		if s, m := e.do(t, "POST", "/gmail/labels", map[string]any{"name": bad}); s != 422 || m["code"] != "invalid_label" {
			t.Fatalf("%q: %d %v", bad, s, m)
		}
	}
	if s, _ := e.do(t, "POST", "/gmail/labels", map[string]any{"name": "x", "extra": 1}); s != 400 {
		t.Fatalf("unknown field: %d", s)
	}

	// Gmail down -> 502, signed out -> 409.
	e.fake.Fail("GET /gmail/v1/users/me/labels", gmailtest.Failure{Status: 500})
	if s, m := e.do(t, "GET", "/gmail/labels", nil); s != 502 || m["code"] != "gmail_unavailable" {
		t.Fatalf("%d %v", s, m)
	}
	e.fake.RevokeGrant()
	e.svc.mu.Lock()
	e.svc.access = nil
	e.svc.mu.Unlock()
	if s, m := e.do(t, "GET", "/gmail/labels", nil); s != 409 || m["code"] != "gmail_needs_reauth" {
		t.Fatalf("%d %v", s, m)
	}
}

func TestFiltersListAndImport(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	acme := e.business(t, "acme.com")
	e.connect(t, "c1")
	inv := e.fake.AddLabel("Acme/Invoices")
	_ = inv
	e.fake.AddFilter(gmailtest.Filter{ID: "fA", From: "@stripe.com", AddLabel: "Acme/Invoices", Archive: true})
	e.fake.AddFilter(gmailtest.Filter{ID: "fB", To: "invoices@acme.com", Subject: "Invoice March", AddLabel: "Acme/Invoices", MarkRead: true})
	e.fake.AddFilter(gmailtest.Filter{ID: "fC", From: "spam@x.com", Delete: true})
	e.fake.AddFilter(gmailtest.Filter{ID: "fD", From: "boss@x.com", AddLabel: "Acme/Invoices", Forward: "me@y.com"})
	e.fake.AddFilter(gmailtest.Filter{ID: "fE", To: "x@other.com", AddLabel: "Acme/Invoices"})
	e.fake.AddFilter(gmailtest.Filter{ID: "fF", Subject: "Receipt (2024)", AddLabel: "Acme/Invoices"})

	s, m := e.do(t, "GET", "/gmail/filters", nil)
	if s != 200 || m["importable"] != float64(2) {
		t.Fatalf("list: %d %v", s, m)
	}
	reasons := map[string]string{}
	for _, f := range m["filters"].([]any) {
		f := f.(map[string]any)
		if r, ok := f["reason"].(string); ok {
			reasons[f["id"].(string)] = r
		}
		if f["id"] == "fB" {
			rule := f["rule"].(map[string]any)
			if rule["business_id"] != acme.ID.String() || rule["match_to_local"] != "invoices" || rule["match_subject"] != "Invoice March" || rule["gmail_label"] != "Acme/Invoices" {
				t.Fatalf("preview %v", rule)
			}
			if f["mark_read"] != true || f["importable"] != true || f["already_imported"] != false {
				t.Fatalf("fB %v", f)
			}
		}
	}
	want := map[string]string{"fC": "deletes_mail", "fD": "forwards_mail", "fE": "unmanaged_domain", "fF": "unsupported_criteria"}
	for id, r := range want {
		if reasons[id] != r {
			t.Fatalf("reasons %v", reasons)
		}
	}

	// Import everything importable.
	s, m = e.do(t, "POST", "/rules:import", map[string]any{})
	if s != 200 || len(m["imported"].([]any)) != 2 || len(m["skipped"].([]any)) != 0 {
		t.Fatalf("import: %d %v", s, m)
	}
	rows, err := e.pool.Query(ctx, `SELECT imported_from_filter, label_only, gmail_archive, gmail_mark_read, gmail_label, business_id, match_to_local, match_from, match_subject, enabled
FROM rules ORDER BY imported_from_filter`)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		filter                string
		labelOnly, arch, read bool
		label                 string
		biz                   *uuid.UUID
		to, from, subj        *string
		enabled               bool
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.filter, &r.labelOnly, &r.arch, &r.read, &r.label, &r.biz, &r.to, &r.from, &r.subj, &r.enabled); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	if len(got) != 2 || got[0].filter != "fA" || got[1].filter != "fB" {
		t.Fatalf("rules %+v", got)
	}
	for _, r := range got {
		if !r.labelOnly || r.arch || r.read || !r.enabled || r.label != "Acme/Invoices" {
			t.Fatalf("imported rule must be label-only: %+v", r)
		}
	}
	if *got[0].from != "@stripe.com" || got[0].biz != nil || *got[1].to != "invoices" || *got[1].biz != acme.ID || *got[1].subj != "Invoice March" {
		t.Fatalf("mapping: %+v %+v", got[0], got[1])
	}

	// Listing now flags them; re-import is a no-op; explicit ids report why.
	_, m = e.do(t, "GET", "/gmail/filters", nil)
	if m["importable"] != float64(0) {
		t.Fatalf("importable after import: %v", m["importable"])
	}
	_, m = e.do(t, "POST", "/rules:import", nil)
	if len(m["imported"].([]any)) != 0 || len(m["skipped"].([]any)) != 2 {
		t.Fatalf("reimport: %v", m)
	}
	s, m = e.do(t, "POST", "/rules:import", map[string]any{"filter_ids": []string{"fC", "fA", "nope", "fC"}})
	skipped := map[string]string{}
	for _, x := range m["skipped"].([]any) {
		x := x.(map[string]any)
		skipped[x["filter_id"].(string)] = x["reason"].(string)
	}
	if s != 200 || len(skipped) != 3 || skipped["fC"] != "deletes_mail" || skipped["fA"] != "already_imported" || skipped["nope"] != "not_found" {
		t.Fatalf("explicit: %d %v", s, m)
	}
	var n int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM rules`).Scan(&n)
	if n != 2 {
		t.Fatalf("rules = %d", n)
	}
}

func TestFiltersScopeMissing(t *testing.T) {
	e := setup(t)
	e.connect(t, "c1")
	e.fake.Fail("GET /gmail/v1/users/me/settings/filters", gmailtest.Failure{Status: 403,
		Body: `{"error":{"code":403,"message":"Request had insufficient authentication scopes.","errors":[{"reason":"insufficientPermissions"}]}}`})
	s, m := e.do(t, "GET", "/gmail/filters", nil)
	if s != 403 || m["code"] != "gmail_scope_missing" {
		t.Fatalf("%d %v", s, m)
	}
}

func eml(from, to, msgID, inReplyTo, subject, body string) []byte {
	h := fmt.Sprintf("From: %s\r\nTo: %s\r\nMessage-ID: <%s>\r\nSubject: %s\r\nDate: Mon, 02 Jan 2026 10:00:00 +0000\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n", from, to, msgID, subject)
	if inReplyTo != "" {
		h += "In-Reply-To: <" + inReplyTo + ">\r\nReferences: <" + inReplyTo + ">\r\n"
	}
	return []byte(h + "\r\n" + body + "\r\n")
}

func TestThreadHydrateImportsSentReplies(t *testing.T) {
	e := setup(t)
	if e.ing == nil {
		t.Skip("TEST_S3_ENDPOINT not set")
	}
	ctx := context.Background()
	acme := e.business(t, "acme.com")
	e.connect(t, "c1")

	// Inbound mail arrives through the normal ingest path.
	inRaw := eml("Pat <pat@client.com>", "invoices@acme.com", "in1@client.com", "", "Invoice question", "Where is my invoice?")
	p, err := parse.Parse(inRaw)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.blob.Put(ctx, "raw/2026/01/in1.eml", strings.NewReader(string(inRaw)), int64(len(inRaw)), nil); err != nil {
		t.Fatal(err)
	}
	res, err := e.ing.Ingest(ctx, p, ingest.RawOpts{Key: "raw/2026/01/in1.eml", EnvTo: "invoices@acme.com", ReceivedAt: time.Now().UTC(), Size: len(inRaw), Source: "edge"})
	if err != nil || !res.Inserted {
		t.Fatalf("ingest: %+v %v", res, err)
	}
	inID := res.MessageID

	// Gmail has the inbound message and your sent reply in one thread.
	e.fake.AddMessage(gmailtest.Msg{ID: "gin", ThreadID: "th1", MessageID: "in1@client.com"})
	sentRaw := eml("Acme Billing <invoices@acme.com>", "pat@client.com", "out1@acme.com", "in1@client.com", "Re: Invoice question", "It is attached.")
	e.fake.AddMessage(gmailtest.Msg{ID: "gout", ThreadID: "th1", MessageID: "out1@acme.com", Raw: sentRaw, Labels: map[string]bool{"SENT": true}, Date: time.Now().Add(time.Minute)})

	// The write-back lookup finds the inbound message and asks for hydration.
	var threads []string
	e.eng.OnFound = func(_ context.Context, th string) { threads = append(threads, th) }
	if err := e.st.WantGmail(ctx, inID, nil, true, false, 0); err != nil {
		t.Fatal(err)
	}
	e.run(t)
	if len(threads) != 1 || threads[0] != "th1" {
		t.Fatalf("OnFound threads: %v", threads)
	}

	w := &HydrateWorker{Svc: e.svc, Store: e.st, Blob: e.blob, Ingest: e.ing}
	n, err := w.Hydrate(ctx, "th1")
	if err != nil || n != 1 {
		t.Fatalf("hydrate: %d %v", n, err)
	}
	var dir, src, key, bizID, thread string
	var read bool
	var gid, rawKey *string
	err = e.pool.QueryRow(ctx, `SELECT direction::text, source::text, is_read, gmail_id, raw_key, business_id::text, thread_key, route::text
FROM messages WHERE message_id_hdr='out1@acme.com'`).Scan(&dir, &src, &read, &gid, &rawKey, &bizID, &thread, &key)
	if err != nil {
		t.Fatal(err)
	}
	var inThread string
	_ = e.pool.QueryRow(ctx, `SELECT thread_key FROM messages WHERE id=$1`, inID).Scan(&inThread)
	if dir != "out" || src != "gmail_thread" || !read || *gid != "gout" || *rawKey != "gmail/gout.eml" || bizID != acme.ID.String() || thread != inThread || key != "routed" {
		t.Fatalf("sent reply stored wrong: dir=%s src=%s read=%v gid=%v raw=%v biz=%s threadMatch=%v route=%s", dir, src, read, gid, rawKey, bizID, thread == inThread, key)
	}
	var gt *string
	_ = e.pool.QueryRow(ctx, `SELECT gmail_thread_id FROM messages WHERE message_id_hdr='out1@acme.com'`).Scan(&gt)
	if gt == nil || *gt != "th1" {
		t.Fatalf("gmail_thread_id = %v", gt)
	}
	if _, err := e.blob.Get(ctx, "gmail/gout.eml"); err != nil {
		t.Fatalf("raw not stored: %v", err)
	}
	// No gmail_sync work for sent mail.
	var syncs int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM gmail_sync gs JOIN messages m ON m.id=gs.message_id WHERE m.direction='out'`).Scan(&syncs)
	if syncs != 0 {
		t.Fatalf("sent mail queued %d syncs", syncs)
	}

	// Idempotent.
	if n, err := w.Hydrate(ctx, "th1"); err != nil || n != 0 {
		t.Fatalf("second hydrate: %d %v", n, err)
	}

	// The reader's thread shows both; lists and unread counts show only inbound.
	s, m := e.do(t, "GET", "/messages/"+inID.String(), nil)
	if s != 200 {
		t.Fatalf("get: %d", s)
	}
	th := m["thread"].([]any)
	if len(th) != 2 || th[0].(map[string]any)["direction"] != "in" || th[1].(map[string]any)["direction"] != "out" {
		t.Fatalf("thread: %v", th)
	}
	s, m = e.do(t, "GET", "/messages?business="+acme.ID.String(), nil)
	if s != 200 || len(m["messages"].([]any)) != 1 {
		t.Fatalf("list must hide sent mail: %d %v", s, m)
	}
}

func TestHydrateUnknownThreadIsNoop(t *testing.T) {
	e := setup(t)
	if e.ing == nil {
		t.Skip("TEST_S3_ENDPOINT not set")
	}
	e.connect(t, "c1")
	w := &HydrateWorker{Svc: e.svc, Store: e.st, Blob: e.blob, Ingest: e.ing}
	if n, err := w.Hydrate(context.Background(), "ghost"); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
}
