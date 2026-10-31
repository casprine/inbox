package ingest

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest/parse"
	"github.com/casprine/inbox/server/internal/store"
)

// RawOpts says how a parsed message enters the store. The edge path uses only Key, EnvTo,
// ReceivedAt, Size and Source; Gmail imports (thread hydration, backfill) set the rest.
type RawOpts struct {
	Key         string    // blob key of the raw message (also the ingest-log key)
	EnvTo       string    // envelope recipient (edge only)
	ReceivedAt  time.Time // when the mail arrived
	Size        int
	Source      string // edge | backfill | gmail_thread
	Direction   string // in (default) | out: your own sent reply; never routed, always read
	GmailID     string
	GmailThread string
	// IsRead marks imported mail as already read (edge mail is always unread).
	IsRead bool
	// RequireBusiness skips (stores nothing) a message whose recipients are on no managed domain.
	RequireBusiness bool
	// LabelsOnly makes the Gmail write-backs this message queues apply labels only (backfill).
	LabelsOnly bool
}

// Result reports what Ingest did.
type Result struct {
	MessageID uuid.UUID
	Inserted  bool
	// Skipped is "duplicate" or "no_business" when nothing was stored.
	Skipped string
}

// Ingest stores a parsed message: recipient → business, threading, attachments, search row,
// routing and the mail.new notification. It is the single path used by edge mail, thread
// hydration and backfill, so all of them are routed and searched identically.
func (w *Worker) Ingest(ctx context.Context, p *parse.Parsed, o RawOpts) (Result, error) {
	var res Result
	if o.Source == "" {
		o.Source = "edge"
	}
	out := o.Direction == "out"
	a := o // the ingest-log key and raw key are o.Key
	err := w.Store.WithTx(ctx, func(q *store.Queries) error {
		res = Result{}
		// recipient candidates, envelope first (for sent mail the sender's own address leads)
		var cands []string
		seen := map[string]bool{}
		add := func(s string) {
			s = strings.ToLower(strings.TrimSpace(s))
			if s != "" && !seen[s] {
				seen[s] = true
				cands = append(cands, s)
			}
		}
		if out {
			add(p.From.Addr)
		}
		add(a.EnvTo)
		for _, d := range p.DeliveredTo {
			add(d)
		}
		for _, x := range p.To {
			add(x.Addr)
		}
		for _, x := range p.Cc {
			add(x.Addr)
		}

		var biz *store.Business
		var toLocal, toTag string
		for _, c := range cands {
			l, t, dom := parse.SplitAddress(c)
			if dom == "" {
				continue
			}
			b, err := q.GetBusinessByDomain(ctx, dom)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if b.Enabled {
				biz, toLocal, toTag = &b, l, t
				break
			}
		}
		if biz == nil && o.RequireBusiness {
			res.Skipped = "no_business"
			return nil
		}
		var bizID *uuid.UUID
		bizKey := ""
		if biz != nil {
			bizID = &biz.ID
			bizKey = biz.ID.String()
		} else if len(cands) > 0 {
			toLocal, toTag, _ = parse.SplitAddress(cands[0])
		}

		// Threads found via References/In-Reply-To keep the stored key; the subject
		// fallback is scoped by business so generic subjects don't merge across them.
		var lookupErr error
		hit := false
		tk := parse.ThreadKey(p, func(hdrs []string) (string, bool) {
			k, ok, err := q.FindThreadKey(ctx, hdrs)
			if err != nil {
				lookupErr = err
			}
			hit = ok
			return k, ok
		})
		if lookupErr != nil {
			return lookupErr
		}
		if !hit {
			sum := sha1.Sum([]byte(parse.NormalizeSubject(p.Subject) + bizKey))
			tk = "subj:" + hex.EncodeToString(sum[:])
		}
		if out && bizID == nil && hit {
			// A reply to a known thread belongs to the thread's business even if its addresses are elsewhere.
			if tb, err := q.ThreadBusiness(ctx, tk); err != nil {
				return err
			} else if tb != nil {
				bizID = tb
			}
		}

		authJSON := []byte(fmt.Sprintf(`{"spf":%q,"dkim":%q,"dmarc":%q}`, p.Auth.SPF, p.Auth.DKIM, p.Auth.DMARC))
		nAtt := 0
		var names []string
		for _, at := range p.Attachments {
			if !at.Inline {
				nAtt++
			}
			if at.Filename != "" {
				names = append(names, at.Filename)
			}
		}
		strp := func(s string) *string {
			if s == "" {
				return nil
			}
			return &s
		}
		env := strings.ToLower(a.EnvTo)
		size := o.Size
		rawKey := o.Key
		m := store.Message{
			ID: ids.NewV7(), BusinessID: bizID, Source: o.Source, Direction: map[bool]string{true: "out", false: "in"}[out],
			State: "stored", MessageIDHdr: p.MessageID,
			EnvTo: strp(env), EnvToAll: cands, ToLocal: strp(toLocal), ToTag: strp(toTag),
			FromAddr: strp(p.From.Addr), FromName: strp(p.From.Name), Subject: strp(p.Subject), Snippet: strp(p.Snippet),
			InReplyTo: strp(p.InReplyTo), ThreadKey: tk, ReceivedAt: o.ReceivedAt, SizeBytes: &size, AttachmentCount: nAtt,
			RawKey: &rawKey, AuthResults: authJSON, Route: "pending", IsRead: o.IsRead || out,
			GmailID: strp(o.GmailID), GmailThreadID: strp(o.GmailThread),
		}
		inserted, err := q.InsertMessage(ctx, m)
		if err != nil {
			return err
		}
		if !inserted {
			res.Skipped = "duplicate"
			return q.AppendIngestLog(ctx, a.Key, nil, "info", "received "+humanSize(size)+"; duplicate Message-ID, skipped")
		}
		res.MessageID, res.Inserted = m.ID, true
		if err := q.AppendIngestLog(ctx, a.Key, &m.ID, "info", "received "+humanSize(size)); err != nil {
			return err
		}

		var put []string
		for _, at := range p.Attachments {
			key := fmt.Sprintf("att/%s/%d", m.ID, at.Index)
			if err := w.Blob.Put(ctx, key, bytes.NewReader(at.Bytes()), int64(len(at.Bytes())), nil); err != nil {
				for _, k := range put {
					_ = w.Blob.Delete(context.WithoutCancel(ctx), k)
				}
				return err
			}
			put = append(put, key)
			sz := at.Size
			if err := q.InsertAttachment(ctx, store.Attachment{
				ID: ids.NewV7(), MessageID: m.ID, Filename: strp(at.Filename), Mime: strp(at.MIME), SizeBytes: &sz,
				ContentID: strp(at.ContentID), Inline: at.Inline, R2Key: key,
			}); err != nil {
				return err
			}
		}

		body := p.Text
		if len(body) > maxSearchBody {
			body = strings.ToValidUTF8(body[:maxSearchBody], "")
		}
		if err := q.UpsertMessageSearch(ctx, m.ID, p.Subject, p.From.Name+" "+p.From.Addr, body, strings.Join(names, " ")); err != nil {
			return err
		}
		if err := q.AppendIngestLog(ctx, a.Key, &m.ID, "info", "stored"); err != nil {
			return err
		}

		if out {
			if err := q.SetMessageRoute(ctx, m.ID, bizID, "routed", nil); err != nil {
				return err
			}
			if err := q.AppendIngestLog(ctx, a.Key, &m.ID, "info", "your sent reply, imported from Gmail"); err != nil {
				return err
			}
			return q.NotifyMailNew(ctx, m.ID)
		}

		d, err := w.Router.Route(ctx, q, m, p)
		if err != nil {
			return err
		}
		if err := q.SetMessageRoute(ctx, m.ID, d.BusinessID, d.Route, d.RuleID); err != nil {
			return err
		}
		if o.LabelsOnly {
			if err := q.MarkGmailSyncLabelsOnly(ctx, m.ID); err != nil {
				return err
			}
		}
		if d.BusinessID != nil {
			if toLocal != "" {
				if err := q.UpsertBusinessAddress(ctx, *d.BusinessID, toLocal, o.ReceivedAt); err != nil {
					return err
				}
			}
			if err := q.TouchBusinessLastMessage(ctx, *d.BusinessID, o.ReceivedAt); err != nil {
				return err
			}
		}
		line := d.Route
		if biz != nil {
			line += " (" + biz.Domain + ")"
		}
		if err := q.AppendIngestLog(ctx, a.Key, &m.ID, "info", line); err != nil {
			return err
		}
		return q.NotifyMailNew(ctx, m.ID)
	})
	return res, err
}
