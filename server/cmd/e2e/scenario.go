package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/gmail/gmailtest"
	"github.com/casprine/inbox/server/internal/ingest/parse"
)

// startSSE follows /v1/events and records every event.
func (d *driver) startSSE(ctx context.Context) (connected func() bool) {
	ready := make(chan struct{})
	go func() {
		req, _ := http.NewRequestWithContext(ctx, "GET", d.base+"/v1/events", nil)
		req.Header.Set("Authorization", "Bearer "+d.token)
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		signalled := false
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, ": connected") && !signalled {
				signalled = true
				close(ready)
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				var ev map[string]any
				if json.Unmarshal([]byte(data), &ev) == nil {
					d.sseMu.Lock()
					d.sse = append(d.sse, ev)
					d.sseMu.Unlock()
				}
			}
		}
	}()
	return func() bool {
		select {
		case <-ready:
			return true
		default:
			return false
		}
	}
}

func (d *driver) events(match func(map[string]any) bool) int {
	d.sseMu.Lock()
	defer d.sseMu.Unlock()
	n := 0
	for _, e := range d.sse {
		if match(e) {
			n++
		}
	}
	return n
}

func (d *driver) idByHdr(hdr string) string {
	var id string
	if err := d.pool.QueryRow(context.Background(), `SELECT id::text FROM messages WHERE message_id_hdr=$1`, hdr).Scan(&id); err != nil {
		panic(fmt.Errorf("no message %s: %v", hdr, err))
	}
	return id
}

func addrs(p *parse.Parsed) []string {
	var out []string
	for _, a := range append(append([]parse.Address{}, p.To...), p.Cc...) {
		out = append(out, strings.ToLower(a.Addr))
	}
	return out
}

func (d *driver) scenario(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	wait := func(name string, timeout time.Duration, f func() bool) {
		if e := d.waitFor(name, timeout, f); e != nil {
			panic(e)
		}
	}

	// ---- access and live events ----
	if s, _, _ := d.get("/v1/businesses", false); s != 401 {
		panic(fmt.Errorf("/v1 without a token answered %d", s))
	}
	d.step("the API refuses requests without a device token")
	// Two listeners: the app API, and a separate one that carries only the Worker's notify (published on its own).
	if s, _, _ := d.doAt(d.base, "POST", "/ingest/v1/notify", []byte("{}"), false, nil); s != http.StatusNotFound {
		panic(fmt.Errorf("the API listener serves /ingest/v1/notify (HTTP %d)", s))
	}
	for _, path := range []string{"/v1/businesses", "/healthz", "/readyz", "/v1/events", "/v1/auth/login"} {
		if s, _, _ := d.doAt(d.ingestBase, "GET", path, nil, true, nil); s != http.StatusNotFound {
			panic(fmt.Errorf("the ingest listener answered %s with HTTP %d, want 404", path, s))
		}
	}
	if s, _, _ := d.doAt(d.ingestBase, "POST", "/v1/auth/login", J{"password": ownerPassword}, false, nil); s != http.StatusNotFound {
		panic(fmt.Errorf("the ingest listener serves login (HTTP %d)", s))
	}
	if s, _, _ := d.doAt(d.ingestBase, "POST", "/ingest/v1/notify", []byte("{}"), false, nil); s != http.StatusUnauthorized {
		panic(fmt.Errorf("unsigned notify on the ingest listener answered %d, want 401", s))
	}
	d.step("the ingest listener serves only a signed /ingest/v1/notify; the API listener does not serve it")

	// Wrong passwords back off and then lock (per client); another client is unaffected.
	var locked bool
	for i := 0; i < 8 && !locked; i++ {
		s, b, h := d.do("POST", "/v1/auth/login", J{"password": "wrong password attempt"}, false, map[string]string{"X-E2E-Client": "203.0.113.50"})
		if s == http.StatusTooManyRequests {
			locked = h.Get("Retry-After") != "" && !strings.Contains(string(b), ownerPassword)
		}
	}
	d.check(locked, "repeated wrong passwords never produced 429 with Retry-After")
	if s, _, h := d.do("POST", "/v1/auth/login", J{"password": ownerPassword}, false, map[string]string{"X-E2E-Client": "203.0.113.50"}); s != http.StatusTooManyRequests || h.Get("Retry-After") == "" {
		panic(fmt.Errorf("the locked client logged in or got no Retry-After: HTTP %d", s))
	}
	time.Sleep(2 * time.Second) // let the global attempt cap refill
	if s, _, _ := d.do("POST", "/v1/auth/login", J{"password": ownerPassword, "device_name": "second phone"}, false, map[string]string{"X-E2E-Client": "203.0.113.51"}); s != http.StatusOK {
		panic(fmt.Errorf("a different client was refused: HTTP %d", s))
	}
	d.step("repeated wrong passwords lock that client out with 429 + Retry-After; other clients still sign in")

	sseCtx, stopSSE := context.WithCancel(ctx)
	defer stopSSE()
	connected := d.startSSE(sseCtx)
	wait("the live event stream connects", 5*time.Second, connected)

	// ---- businesses and rules (Cloudflare fake) ----
	doms := d.must("GET", "/v1/cloudflare/domains", nil, 200)
	d.check(at(doms, "domains", 0, "domain") == domain && at(doms, "domains", 0, "selectable") == true && at(doms, "domains", 0, "routing") == "ok",
		"domain list: %v", doms)
	d.step("Cloudflare domain list shows the routed domain as selectable")
	biz := d.must("POST", "/v1/businesses", J{"name": "My Domain", "domain": domain}, 201)
	d.bizID = biz["id"].(string)
	d.check(biz["routing"] == "ok", "business routing: %v", biz)
	d.step("a business is created and its routing verified against Cloudflare")
	d.must("POST", "/v1/rules", J{"name": "Shop orders", "business_id": d.bizID, "match_to_local": "shop", "gmail_label": "Shop/Orders", "label_only": true}, 201)
	d.step("a rule is created")

	// ---- the Worker delivers mail ----
	deliveries := []struct{ name, envTo string }{
		{"plain", "shop+orders@mydomain.com"}, {"reply", "shop+orders@mydomain.com"},
		{"attachment", "a@mydomain.com"}, {"alternative", "a@mydomain.com"}, {"inline_cid", "a@mydomain.com"},
	}
	for _, x := range deliveries {
		if e := d.workerDelivers(ctx, x.name, d.fixture(x.name), x.envTo, true); e != nil {
			panic(e)
		}
	}
	wait("five delivered mails are stored", 30*time.Second, func() bool { return d.count(`SELECT count(*) FROM messages WHERE state='stored'`) == 5 })
	wait("routing finishes for all of them", 15*time.Second, func() bool { return d.count(`SELECT count(*) FROM messages WHERE route='pending'`) == 0 })

	bs := d.must("GET", "/v1/businesses", nil, 200)
	d.check(at(bs, "businesses", 0, "unread") == float64(5), "unread count: %v", bs)
	d.step("the business shows 5 unread")
	att := d.must("GET", "/v1/attention", nil, 200)
	d.check(at(att, "unrouted", "count") == float64(3), "attention: %v", att)
	d.step("3 messages need a rule, 2 matched the rule")
	plainID, replyID := d.idByHdr("plain-1@example.com"), d.idByHdr("reply-1@example.net")
	m := d.must("GET", "/v1/messages/"+plainID, nil, 200)
	d.check(at(m, "details", "rule_name") == "Shop orders" && at(m, "details", "business", "domain") == domain, "plain details: %v", at(m, "details"))
	d.step("a message shows its business and the rule that matched")
	m = d.must("GET", "/v1/messages/"+replyID, nil, 200)
	d.check(len(m["thread"].([]any)) == 2, "thread: %v", m["thread"])
	d.step("a reply is threaded with the message it answers")

	altID := d.idByHdr("alt-1@example.org")
	body := d.must("GET", "/v1/messages/"+altID+"/body", nil, 200)
	d.check(body["html"] != nil && body["text"] != nil, "body: %v", body)
	d.step("a multipart/alternative message serves both HTML and text")

	rawStatus, rawBytes, rawHdr := d.do("GET", "/v1/messages/"+plainID+"/raw", nil, true, nil)
	if rawStatus == http.StatusFound {
		rawBytes = fetchURL(rawHdr.Get("Location"))
	}
	d.check(bytes.Equal(rawBytes, d.samples["plain"].raw), "raw message differs from what the Worker stored (HTTP %d, %d bytes)", rawStatus, len(rawBytes))
	d.step("the original message is returned byte for byte")

	attMsg := d.must("GET", "/v1/messages/"+d.idByHdr("att-1@example.org"), nil, 200)
	aid := at(attMsg, "attachments", 0, "id").(string)
	as, _, ah := d.do("GET", "/v1/attachments/"+aid, nil, true, nil)
	d.check(as == http.StatusFound && ah.Get("Location") != "", "attachment redirect: HTTP %d", as)
	d.check(len(fetchURL(ah.Get("Location"))) > 0, "presigned attachment URL returned nothing")
	d.step("an attachment downloads through a presigned URL")

	wait("the app hears about every new mail live", 5*time.Second, func() bool {
		return d.events(func(e map[string]any) bool { return e["type"] == "mail.new" }) >= 5
	})

	// duplicate notification: no second copy
	if e := d.workerDelivers(ctx, "plain", d.fixture("plain"), "shop+orders@mydomain.com", true); e != nil {
		panic(e)
	}
	time.Sleep(3 * time.Second)
	d.check(d.count(`SELECT count(*) FROM messages WHERE state='stored'`) == 5, "duplicate notification created a copy")
	d.step("a repeated delivery of the same message is ignored")

	// ---- search ----
	_, sb, _ := d.get("/v1/search?q=invoice", true)
	d.check(strings.Contains(string(sb), "Invoice attached"), "search for 'invoice': %s", sb)
	_, sb, _ = d.get("/v1/search?q=has:attachment", true)
	d.check(strings.Contains(string(sb), "Invoice attached"), "search has:attachment: %s", sb)
	_, sb, _ = d.get("/v1/search?q=from:alice", true)
	d.check(strings.Contains(string(sb), "Hello there"), "search from:alice: %s", sb)
	d.step("search finds mail by word, attachment and sender")

	// ---- a mail the Worker could not store ----
	lostRaw := mailEML("lost-1@client.org", "Pat <pat@client.org>", "shop@mydomain.com", "Lost in transit", "This one failed to reach R2.", time.Now())
	if e := d.workerDelivers(ctx, "lost", lostRaw, "shop@mydomain.com", false); e != nil {
		panic(e)
	}
	wait("a mail the Worker failed to store shows up as failed", 15*time.Second, func() bool {
		_, fm := d.api("GET", "/v1/attention/failed", nil)
		return at(fm, "messages", 0, "failure_reason") == "store_failed"
	})
	_, fm := d.api("GET", "/v1/attention/failed", nil)
	lostFailedID := at(fm, "messages", 0, "id").(string)
	s, rm := d.api("POST", "/v1/failed/"+lostFailedID+":retry", nil)
	d.check(s == 409 && rm["code"] == "needs_gmail", "retry without Gmail: %d %v", s, rm)
	d.step("retrying it without Gmail connected explains that Gmail is needed")

	// ---- Gmail (fake Google) ----
	gm := map[string]*gmailtest.Msg{}
	for name, sm := range d.samples {
		p, _ := parse.Parse(sm.raw)
		gm[name] = d.gfake.AddMessage(gmailtest.Msg{MessageID: sm.messageID, Raw: sm.raw, To: addrs(p), Date: time.Now().Add(-time.Hour)})
	}
	conn := d.must("POST", "/v1/gmail/connect", nil, 200)
	au, _ := url.Parse(conn["auth_url"].(string))
	d.gfake.IssueCode("e2e-code", au.Query().Get("code_challenge"))
	cs, _, ch := d.do("GET", "/oauth/google/callback?"+url.Values{"code": {"e2e-code"}, "state": {au.Query().Get("state")}}.Encode(), nil, false, nil)
	d.check(cs == http.StatusFound && ch.Get("Location") == "inbox://gmail/connected?status=ok", "oauth callback: %d %s", cs, ch.Get("Location"))
	st := d.must("GET", "/v1/gmail/status", nil, 200)
	d.check(st["state"] == "connected" && st["email"] == "sam@gmail.com", "gmail status: %v", st)
	d.step("Gmail connects through the OAuth handshake")

	wait("the rule's label reaches Gmail (and the message stays in the inbox)", 40*time.Second, func() bool {
		return strings.Contains(strings.Join(d.gfake.LabelNames(gm["plain"].ID), ","), "Shop/Orders")
	})
	d.check(d.gfake.Message(gm["plain"].ID).Labels["INBOX"], "label-only rule removed INBOX")

	rs, rimp := d.api("POST", "/v1/failed/"+lostFailedID+":retry", nil)
	d.check(rs == 200 && rimp["status"] == "imported", "retry via Gmail: %d %v", rs, rimp)
	d.check(d.count(`SELECT count(*) FROM messages WHERE message_id_hdr='lost-1@client.org' AND state='stored'`) == 1, "retried message not stored")
	_, fm = d.api("GET", "/v1/attention/failed", nil)
	d.check(len(at(fm, "messages").([]any)) == 0, "failed list not empty after retry: %v", fm)
	d.step("a failed mail is recovered from Gmail with Retry")

	// archive with undo
	attachID := d.idByHdr("att-1@example.org")
	d.must("POST", "/v1/messages:batch", J{"ids": []string{attachID}, "op": "archive"}, 200)
	wait("archiving removes the message from the Gmail inbox after the undo window", 40*time.Second, func() bool {
		return !d.gfake.Message(gm["attachment"].ID).Labels["INBOX"]
	})
	cidID := d.idByHdr("cid-1@example.org")
	d.must("POST", "/v1/messages:batch", J{"ids": []string{cidID}, "op": "archive"}, 200)
	d.must("POST", "/v1/messages:batch", J{"ids": []string{cidID}, "op": "unarchive"}, 200)
	time.Sleep(12 * time.Second)
	d.check(d.gfake.Count("POST /gmail/v1/users/me/messages/"+gm["inline_cid"].ID+"/modify") == 0, "Undo still caused a Gmail change")
	d.check(d.gfake.Message(gm["inline_cid"].ID).Labels["INBOX"], "undone archive changed Gmail")
	d.step("Undo inside the window never touches Gmail")

	// filters -> rules
	d.gfake.AddLabel("Newsletters")
	d.gfake.AddFilter(gmailtest.Filter{From: "@example.org", AddLabel: "Newsletters"})
	flt := d.must("GET", "/v1/gmail/filters", nil, 200)
	d.check(flt["importable"] == float64(1), "filters: %v", flt)
	imp := d.must("POST", "/v1/rules:import", J{}, 200)
	d.check(len(imp["imported"].([]any)) == 1, "import: %v", imp)
	wait("importing a Gmail filter re-routes waiting mail to the new rule", 20*time.Second, func() bool {
		mm := d.must("GET", "/v1/messages/"+altID, nil, 200)
		return at(mm, "details", "rule_name") == "Newsletters"
	})
	_, _ = d.pool.Exec(ctx, `UPDATE gmail_sync SET next_attempt_at = now() WHERE state = 'pending'`) // skip the 30 s settle delay
	wait("the imported rule's label reaches Gmail", 30*time.Second, func() bool {
		return strings.Contains(strings.Join(d.gfake.LabelNames(gm["alternative"].ID), ","), "Newsletters")
	})

	// ---- backfill ----
	for i := 1; i <= 6; i++ {
		raw := mailEML(fmt.Sprintf("old-%d@client.org", i), "Pat <pat@client.org>", fmt.Sprintf("old%d@mydomain.com", i), fmt.Sprintf("Old mail %d", i), "from long ago", time.Now().AddDate(0, 0, -30))
		d.gfake.AddMessage(gmailtest.Msg{MessageID: fmt.Sprintf("old-%d@client.org", i), Raw: raw, To: []string{fmt.Sprintf("old%d@mydomain.com", i)},
			Date: time.Now().AddDate(0, 0, -30), Labels: map[string]bool{"INBOX": true}})
	}
	est := d.must("GET", "/v1/backfills:estimate?days=365", nil, 200)
	d.check(est["total"] == float64(11), "estimate: %v", est) // 4 fixtures + the recovered mail + 6 old ones
	d.step("the older-mail estimate counts what Gmail has for the domain")
	bf := d.must("POST", "/v1/backfills", J{"days": 365}, 201)
	bfID := at(bf, "backfills", 0, "id").(string)
	wait("the older-mail import finishes", 40*time.Second, func() bool {
		l := d.must("GET", "/v1/backfills", nil, 200)
		return at(l, "backfills", 0, "status") == "done"
	})
	l := d.must("GET", "/v1/backfills", nil, 200)
	b0 := at(l, "backfills", 0)
	d.check(at(b0, "id") == bfID && at(b0, "imported") == float64(6) && at(b0, "skipped") == float64(5) && at(b0, "scanned") == float64(11),
		"backfill result: %v", b0)
	d.check(d.count(`SELECT count(*) FROM messages WHERE source='backfill'`) == 6 && d.count(`SELECT count(*) FROM messages WHERE state='stored'`) == 12,
		"message counts after import")
	d.step("older mail is imported, duplicates are skipped")
	wait("import progress was announced live", 5*time.Second, func() bool {
		return d.events(func(e map[string]any) bool { return e["type"] == "job.progress" && e["status"] == "done" }) >= 1
	})

	// ---- health ----
	h := d.must("GET", "/v1/health", nil, 200)
	checks := map[string]string{}
	for _, c := range h["checks"].([]any) {
		checks[at(c, "id").(string)] = at(c, "state").(string)
	}
	d.check(checks["gmail"] == "ok" && checks["mail_arriving"] == "ok" && checks["worker"] == "ok" && checks["queue"] == "ok", "health: %v", h)
	d.step("the Health screen reports everything ok")

	// ---- Alfather over MCP ----
	pair := d.must("POST", "/v1/alfather/pairings", nil, 201)
	tok := pair["token"].(string)
	mcp := func(method string, params any) J {
		body, _ := json.Marshal(J{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		s, b, _ := d.do("POST", "/mcp", body, false, map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json"})
		d.check(s == 200, "mcp %s: HTTP %d %s", method, s, b)
		var m J
		_ = json.Unmarshal(b, &m)
		return m
	}
	init := mcp("initialize", J{"protocolVersion": "2025-06-18", "capabilities": J{}, "clientInfo": J{"name": "e2e", "version": "1"}})
	d.check(at(init, "result", "serverInfo", "name") == "inbox", "initialize: %v", init)
	call := func(tool string, args J) J {
		r := mcp("tools/call", J{"name": tool, "arguments": args})
		d.check(r["error"] == nil && at(r, "result", "isError") == false, "tool %s: %v", tool, r)
		return at(r, "result", "structuredContent").(map[string]any)
	}
	d.check(len(call("next_messages", J{})["messages"].([]any)) == 0, "a fresh pairing must start at the newest mail")
	if e := d.workerDelivers(ctx, "late", mailEML("late-1@client.org", "Pat <pat@client.org>", "shop@mydomain.com", "Late order", "One more order.", time.Now()), "shop@mydomain.com", true); e != nil {
		panic(e)
	}
	var feed J
	wait("Alfather is offered the new mail", 20*time.Second, func() bool {
		feed = call("next_messages", J{})
		return len(feed["messages"].([]any)) == 1
	})
	first := at(feed, "messages", 0)
	d.check(at(first, "subject") == "Late order" && at(first, "rule") == "Shop orders" && at(first, "business", "domain") == domain, "feed message: %v", first)
	ack := call("ack", J{"up_to_seq": at(first, "seq")})
	d.check(ack["waiting"] == float64(0), "ack: %v", ack)
	ast := d.must("GET", "/v1/alfather/status", nil, 200)
	d.check(ast["paired"] == true && ast["connected"] == true && ast["waiting"] == float64(0), "alfather status: %v", ast)
	d.step("Alfather reads new mail over MCP and acknowledges it")

	// ---- settings, disconnect, delete everything ----
	pf := d.must("PUT", "/v1/prefs", J{"preview_lines": 3, "retention_days": 365}, 200)
	d.check(pf["preview_lines"] == float64(3) && pf["retention_days"] == float64(365), "prefs: %v", pf)
	d.step("preferences are saved")

	d.must("POST", "/v1/gmail:disconnect", nil, 204)
	st = d.must("GET", "/v1/gmail/status", nil, 200)
	d.check(st["state"] == "not_connected" && len(d.gfake.Revoked) == 1, "disconnect: %v revoked=%v", st, d.gfake.Revoked)
	d.step("Gmail disconnects and the grant is revoked at Google")

	// ---- catch-up: a mail reached R2 but the Worker's ping never arrived ----
	missed := mailEML("missed-1@client.org", "Pat <pat@client.org>", "shop@mydomain.com", "Missed ping", "R2 has it, the notify was lost.", time.Now())
	missedKey := fmt.Sprintf("raw/%04d/%02d/01MISSEDPING%s.eml", time.Now().UTC().Year(), int(time.Now().UTC().Month()), strings.ToUpper(uuid.NewString()[:8]))
	d.check(d.blob.Put(ctx, missedKey, bytes.NewReader(missed), int64(len(missed)), map[string]string{"env_from": "pat@client.org", "env_to": "shop@mydomain.com"}) == nil, "put missed mail into R2")
	time.Sleep(1500 * time.Millisecond) // RECONCILE_MIN_AGE is 1s in this run
	d.must("POST", "/v1/mail:refresh", nil, 202)
	wait("the app's refresh finds the mail whose ping was lost", 20*time.Second, func() bool {
		return d.count(`SELECT count(*) FROM messages WHERE message_id_hdr='missed-1@client.org' AND state='stored'`) == 1
	})
	d.step("a mail in R2 whose notify was lost is ingested when the app asks for a refresh")

	tokn := d.must("POST", "/v1/data:confirm", nil, 200)["token"].(string)
	del := d.must("DELETE", "/v1/data", J{"confirm": tokn}, 200)
	d.check(at(del, "deleted_messages").(float64) >= 13, "purge: %v", del)
	d.check(d.count(`SELECT count(*) FROM messages`) == 0, "messages remain after purge")
	for _, prefix := range []string{"raw/", "att/", "gmail/"} {
		infos, err := d.blob.List(ctx, prefix, "")
		d.check(err == nil && len(infos) == 0, "objects remain under %s: %v %v", prefix, len(infos), err)
	}
	d.check(d.count(`SELECT count(*) FROM businesses`) == 1 && d.count(`SELECT count(*) FROM rules`) == 2, "purge removed setup")
	d.step("Delete stored mail removes every message and stored object, keeps the setup")
	return nil
}

func fetchURL(u string) []byte {
	resp, err := http.Get(u)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		panic(fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode))
	}
	return b
}
