package mail

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest"
	"github.com/casprine/inbox/server/internal/ingest/parse"
	"github.com/casprine/inbox/server/internal/rules"
	"github.com/casprine/inbox/server/internal/store"
)

func demoEML(msgID, from, to, subject, inReplyTo, contentType, body string, at time.Time) []byte {
	h := fmt.Sprintf("From: %s\r\nTo: %s\r\nMessage-ID: <%s>\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: %s; charset=utf-8\r\n",
		from, to, msgID, subject, at.Format(time.RFC1123Z), contentType)
	if inReplyTo != "" {
		h += "In-Reply-To: <" + inReplyTo + ">\r\nReferences: <" + inReplyTo + ">\r\n"
	}
	return []byte(h + "\r\n" + body + "\r\n")
}

// seedExtras adds what the app's live tests need beyond the parse fixtures: a rule, a conversation that
// includes a reply you sent, mail whose HTML loads remote images, failed items (one retriable) and a
// paused import. Everything is keyed by stable ids, so re-running changes nothing. Returns messages added.
func seedExtras(ctx context.Context, st *store.Store, bl *blob.Store, w *ingest.Worker, biz []store.Business, now time.Time) (int, error) {
	acme, north := biz[0], biz[1]
	added := 0
	put := func(key string, raw []byte) error {
		return bl.Put(ctx, key, bytes.NewReader(raw), int64(len(raw)), nil)
	}
	exists := func(hdr string) bool {
		ok, _ := st.HasMessageIDHdr(ctx, hdr)
		return ok
	}

	// A rule so some demo mail is routed: orders@ at the first business gets a label.
	rs, err := st.ListRules(ctx)
	if err != nil {
		return 0, err
	}
	have := false
	for _, r := range rs {
		have = have || r.Name == "Demo orders"
	}
	if !have {
		local, label := "orders", "Acme/Orders"
		if _, err := st.CreateRuleTop(ctx, rules.Rule{ID: ids.NewV7(), Name: "Demo orders", Enabled: true, BusinessID: &acme.ID,
			MatchToLocal: &local, GmailLabel: &label, LabelOnly: true}); err != nil {
			return 0, err
		}
	}

	// A conversation: customer asks, you answered (sent reply imported from Gmail), customer says thanks.
	to := "billing@" + acme.Domain
	t0 := now.Add(-30 * time.Hour)
	ask := demoEML("seed-thread-1@client.test", "Dana Client <dana@client.test>", to, "March invoice", "", "text/plain",
		"Hi, could you send the March invoice again? Thanks, Dana", t0)
	reply := demoEML("seed-thread-2@"+acme.Domain, "Acme Billing <"+to+">", "dana@client.test", "Re: March invoice", "seed-thread-1@client.test", "text/plain",
		"Hi Dana, it is attached to the original mail from March 3. Let me know if anything is missing.", t0.Add(time.Hour))
	thanks := demoEML("seed-thread-3@client.test", "Dana Client <dana@client.test>", to, "Re: March invoice", "seed-thread-2@"+acme.Domain, "text/plain",
		"Found it, thank you!", t0.Add(10*time.Hour))
	if !exists("seed-thread-1@client.test") {
		if err := put("raw/demo/seed-thread-1.eml", ask); err != nil {
			return added, err
		}
		if err := w.Process(ctx, ingest.IngestArgs{Key: "raw/demo/seed-thread-1.eml", EnvFrom: "dana@client.test", EnvTo: to,
			RawSize: int64(len(ask)), ReceivedAt: t0.Format(time.RFC3339)}); err != nil {
			return added, err
		}
		added++
	}
	if !exists("seed-thread-2@" + acme.Domain) {
		p, err := parse.Parse(reply)
		if err != nil {
			return added, err
		}
		if err := put("gmail/seed-out-1.eml", reply); err != nil {
			return added, err
		}
		if _, err := w.Ingest(ctx, p, ingest.RawOpts{Key: "gmail/seed-out-1.eml", ReceivedAt: t0.Add(time.Hour), Size: len(reply),
			Source: "gmail_thread", Direction: "out", GmailID: "seed-out-1", GmailThread: "seed-thread", IsRead: true}); err != nil {
			return added, err
		}
	}
	if !exists("seed-thread-3@client.test") {
		if err := put("raw/demo/seed-thread-3.eml", thanks); err != nil {
			return added, err
		}
		if err := w.Process(ctx, ingest.IngestArgs{Key: "raw/demo/seed-thread-3.eml", EnvFrom: "dana@client.test", EnvTo: to,
			RawSize: int64(len(thanks)), ReceivedAt: t0.Add(10 * time.Hour).Format(time.RFC3339)}); err != nil {
			return added, err
		}
		added++
	}

	// Routed by the demo rule.
	if !exists("seed-order-1@shop.test") {
		raw := demoEML("seed-order-1@shop.test", "Shop <shop@shop.test>", "orders@"+acme.Domain, "New order #1042", "", "text/plain",
			"Order #1042: 3 items, total 129.00. Ship by Friday.", now.Add(-5*time.Hour))
		if err := put("raw/demo/seed-order-1.eml", raw); err != nil {
			return added, err
		}
		if err := w.Process(ctx, ingest.IngestArgs{Key: "raw/demo/seed-order-1.eml", EnvFrom: "shop@shop.test", EnvTo: "orders@" + acme.Domain,
			RawSize: int64(len(raw)), ReceivedAt: now.Add(-5 * time.Hour).Format(time.RFC3339)}); err != nil {
			return added, err
		}
		added++
	}

	// Remote images: the app must block them until the person allows the sender.
	if !exists("seed-images-1@promo.test") {
		html := `<html><body><h1>Weekly offers</h1><p>See <a href="https://promo.test/offers">this week</a>.</p>` +
			`<img src="https://tracker.promo.test/pixel.gif?u=1" width="1" height="1" alt="">` +
			`<img src="https://cdn.promo.test/banner.png" alt="Banner" width="600"></body></html>`
		raw := demoEML("seed-images-1@promo.test", "Promo <deals@promo.test>", "hello@"+north.Domain, "Your weekly offers", "", "text/html", html, now.Add(-2*time.Hour))
		if err := put("raw/demo/seed-images-1.eml", raw); err != nil {
			return added, err
		}
		if err := w.Process(ctx, ingest.IngestArgs{Key: "raw/demo/seed-images-1.eml", EnvFrom: "deals@promo.test", EnvTo: "hello@" + north.Domain,
			RawSize: int64(len(raw)), ReceivedAt: now.Add(-2 * time.Hour).Format(time.RFC3339)}); err != nil {
			return added, err
		}
		added++
	}

	// Failed items. (1) the Worker could not store the mail and no copy exists; (2) a copy exists but cannot be read;
	// (3) a copy exists and is fine, so Retry succeeds.
	if !exists("failed:seed-store-failed") {
		if err := w.ProcessFailed(ctx, ingest.IngestArgs{Reason: "store_failed", FailID: "seed-store-failed", EnvFrom: "dana@client.test",
			EnvTo: "billing@" + acme.Domain, ReceivedAt: now.Add(-90 * time.Minute).Format(time.RFC3339), MessageID: "<seed-lost-1@client.test>"}); err != nil {
			return added, err
		}
	}
	failedRow := func(hdr, reason, rawKey string, raw []byte, at time.Time, line string) error {
		if exists(hdr) {
			return nil
		}
		if err := put(rawKey, raw); err != nil {
			return err
		}
		env, from, size := "hello@"+north.Domain, "dana@client.test", len(raw)
		m := store.Message{ID: ids.NewV7(), Source: "edge", State: "failed", FailureReason: &reason, MessageIDHdr: hdr, EnvTo: &env,
			FromAddr: &from, ThreadKey: hdr, Route: "unrouted", ReceivedAt: at, SizeBytes: &size, RawKey: &rawKey}
		return st.WithTx(ctx, func(q *store.Queries) error {
			if ok, err := q.InsertMessage(ctx, m); err != nil || !ok {
				return err
			}
			if err := q.AppendIngestLog(ctx, rawKey, &m.ID, "info", "received "+fmt.Sprint(size)+" B"); err != nil {
				return err
			}
			return q.AppendIngestLog(ctx, rawKey, &m.ID, "error", line)
		})
	}
	if err := failedRow("raw:raw/demo/seed-parse-failed.eml", "parse_failed", "raw/demo/seed-parse-failed.eml",
		[]byte("\x00\x01\x02 this is not an email message"), now.Add(-3*time.Hour), "parse failed: no headers found"); err != nil {
		return added, err
	}
	retryRaw := demoEML("seed-retry-1@client.test", "Dana Client <dana@client.test>", "hello@"+north.Domain, "Retry me", "", "text/plain",
		"This message was saved but its first processing failed; Retry puts it in the inbox.", now.Add(-4*time.Hour))
	if err := failedRow("raw:raw/demo/seed-retriable.eml", "store_failed", "raw/demo/seed-retriable.eml", retryRaw, now.Add(-4*time.Hour),
		"processing was interrupted; the saved copy is intact"); err != nil {
		return added, err
	}

	// A paused older-mail import (waiting out a Gmail rate limit).
	var n int
	if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM backfills WHERE business_id=$1`, acme.ID).Scan(&n); err != nil {
		return added, err
	}
	if n == 0 {
		_, err := st.Pool().Exec(ctx, `INSERT INTO backfills (id, business_id, since, status, estimated, scanned, imported, sorted, unrouted, skipped,
paused_until, last_error, started_at) VALUES ($1,$2,$3,'paused',1200,340,300,240,60,40, now() + interval '1 hour','rate_limited', now() - interval '20 minutes')`,
			ids.NewV7(), acme.ID, now.AddDate(0, 0, -90))
		if err != nil {
			return added, err
		}
	}
	return added, nil
}
