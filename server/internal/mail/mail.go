// Package mail serves the businesses and messages parts of the /v1 API.
package mail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest/parse"
	"github.com/casprine/inbox/server/internal/mail/search"
	"github.com/casprine/inbox/server/internal/store"
)

// Blobs is the object-store surface the API needs.
type Blobs interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	DeleteMany(ctx context.Context, keys []string) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

const (
	archiveUndoWindow = 8 * time.Second
	attachmentTTL     = 60 * time.Second
	maxBatch          = 200
	maxBody           = 1 << 20
)

// API holds handler dependencies.
type API struct {
	Store *store.Store
	Blob  Blobs // nil when object storage is not configured: blob-backed routes answer 503 blob_unavailable
	Log   *slog.Logger
	// Retry re-enqueues ingest_process for a raw key (nil: retry answers 503 queue_unavailable).
	Retry Retrier
	// Refresh queues a catch-up scan of R2 (nil: POST /v1/mail:refresh answers 503 queue_unavailable).
	Refresh Refresher
	// Zones validates a new business's domain against Cloudflare; nil or unconfigured skips the check.
	Zones ZoneResolver
	// Fetch re-imports failed mail that has no stored copy from Gmail (nil: such retries answer 409 needs_gmail).
	Fetch GmailFetcher
}

// ZoneResolver finds the Cloudflare zone for a domain and its Email Routing state.
type ZoneResolver interface {
	Configured() bool
	ResolveZone(ctx context.Context, domain string) (zoneID, routing string, found bool, err error)
}

// blobOK writes a 503 problem and returns false when no blob store is configured.
func (a *API) blobOK(w http.ResponseWriter) bool {
	if a.Blob == nil {
		httpapi.WriteProblem(w, http.StatusServiceUnavailable, "blob_unavailable", "object storage is not configured")
		return false
	}
	return true
}

// Routes registers the endpoints on the protected /v1 group.
func (a *API) Routes(r chi.Router) {
	r.Get("/businesses", a.listBusinesses)
	r.Post("/businesses", a.createBusiness)
	r.Put("/businesses:order", a.orderBusinesses)
	r.Patch("/businesses/{id}", a.patchBusiness)
	r.Delete("/businesses/{id}", a.deleteBusiness)

	r.Post("/mail:refresh", a.refresh)

	r.Get("/messages", a.listMessages)
	r.Post("/messages:batch", a.batch)
	r.Get("/messages/{id}", a.getMessage)
	r.Get("/messages/{id}/body", a.getBody)
	r.Get("/messages/{id}/raw", a.getRaw)
	r.Get("/attachments/{id}", a.getAttachment)

	(&search.API{Store: a.Store}).Routes(r)
	a.attentionRoutes(r)
}

func (a *API) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}

func (a *API) internal(w http.ResponseWriter, err error) {
	a.log().Error("mail api", "err", err)
	httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
}

func bad(w http.ResponseWriter, code, detail string) {
	httpapi.WriteProblem(w, http.StatusBadRequest, code, detail)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		bad(w, "invalid_body", err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpapi.WriteProblem(w, http.StatusNotFound, "not_found", "")
		return id, false
	}
	return id, true
}

func notFound(w http.ResponseWriter) { httpapi.WriteProblem(w, http.StatusNotFound, "not_found", "") }

// ---- businesses ----

type businessJSON struct {
	ID            uuid.UUID     `json:"id"`
	Name          string        `json:"name"`
	Domain        string        `json:"domain"`
	SortOrder     int           `json:"sort_order"`
	Hidden        bool          `json:"hidden"`
	Enabled       bool          `json:"enabled"`
	Routing       string        `json:"routing"`
	Unread        int           `json:"unread"`
	LastMessageAt *time.Time    `json:"last_message_at"`
	Addresses     []addressJSON `json:"addresses"`
}

type addressJSON struct {
	LocalPart string     `json:"local_part"`
	Count     int        `json:"count"`
	LastAt    *time.Time `json:"last_at"`
}

func toBusiness(b store.Business, unread int, addrs []addressJSON) businessJSON {
	if addrs == nil {
		addrs = []addressJSON{}
	}
	return businessJSON{ID: b.ID, Name: b.Name, Domain: b.Domain, SortOrder: b.SortOrder, Hidden: b.Hidden,
		Enabled: b.Enabled, Routing: b.Routing, Unread: unread, LastMessageAt: b.LastMessageAt, Addresses: addrs}
}

func (a *API) listBusinesses(w http.ResponseWriter, r *http.Request) {
	bs, err := a.Store.ListBusinessesWithUnread(r.Context())
	if err != nil {
		a.internal(w, err)
		return
	}
	addrs, err := a.Store.ListAllBusinessAddresses(r.Context())
	if err != nil {
		a.internal(w, err)
		return
	}
	by := map[uuid.UUID][]addressJSON{}
	for _, ad := range addrs {
		by[ad.BusinessID] = append(by[ad.BusinessID], addressJSON{ad.LocalPart, ad.MsgCount, ad.LastAt})
	}
	out := make([]businessJSON, 0, len(bs))
	for _, b := range bs {
		out = append(out, toBusiness(b.Business, b.Unread, by[b.ID]))
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"businesses": out})
}

var domainRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

func (a *API) createBusiness(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		Domain   string `json:"domain"`
		CFZoneID string `json:"cf_zone_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !domainRE.MatchString(in.Domain) {
		bad(w, "invalid_business", "name and a valid domain are required")
		return
	}
	routing := ""
	if a.Zones != nil && a.Zones.Configured() {
		zone, state, found, err := a.Zones.ResolveZone(r.Context(), in.Domain)
		if err != nil {
			a.log().Warn("cloudflare zone lookup failed", "err", err)
			httpapi.WriteProblem(w, http.StatusBadGateway, "cloudflare_unavailable", "Could not reach Cloudflare to check this domain")
			return
		}
		if !found {
			httpapi.WriteProblem(w, http.StatusUnprocessableEntity, "domain_not_in_cloudflare", "This domain is not a zone in the connected Cloudflare account")
			return
		}
		in.CFZoneID, routing = zone, state
	}
	var created store.Business
	err := a.Store.WithTx(r.Context(), func(q *store.Queries) error {
		n, err := q.NextBusinessSortOrder(r.Context())
		if err != nil {
			return err
		}
		created, err = q.CreateBusiness(r.Context(), store.Business{ID: ids.NewV7(), Name: in.Name, Domain: in.Domain,
			CFZoneID: in.CFZoneID, SortOrder: n, Enabled: true, Routing: routing})
		return err
	})
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		httpapi.WriteProblem(w, http.StatusConflict, "domain_taken", "a business already uses this domain")
		return
	}
	if err != nil {
		a.internal(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, toBusiness(created, 0, nil))
}

func (a *API) patchBusiness(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Name    *string `json:"name"`
		Hidden  *bool   `json:"hidden"`
		Enabled *bool   `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			bad(w, "invalid_business", "name cannot be empty")
			return
		}
		in.Name = &n
	}
	b, err := a.Store.UpdateBusiness(r.Context(), id, store.BusinessPatch{Name: in.Name, Hidden: in.Hidden, Enabled: in.Enabled})
	if errors.Is(err, store.ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		a.internal(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, toBusiness(b, 0, nil))
}

func (a *API) orderBusinesses(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []uuid.UUID `json:"ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := a.Store.SetBusinessOrder(r.Context(), in.IDs); err != nil {
		a.internal(w, err)
		return
	}
	a.listBusinesses(w, r)
}

func (a *API) deleteBusiness(w http.ResponseWriter, r *http.Request) {
	if !a.blobOK(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := a.Store.GetBusiness(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		notFound(w)
		return
	} else if err != nil {
		a.internal(w, err)
		return
	}
	keys, err := a.Store.BusinessBlobKeys(r.Context(), id)
	if err != nil {
		a.internal(w, err)
		return
	}
	// Rows first: a failed blob purge leaves orphans (harmless, listed by reconcile), never dangling rows.
	err = a.Store.WithTx(r.Context(), func(q *store.Queries) error {
		if err := q.DeleteBusinessMessages(r.Context(), id); err != nil {
			return err
		}
		return q.DeleteBusiness(r.Context(), id)
	})
	if err != nil {
		a.internal(w, err)
		return
	}
	if err := a.Blob.DeleteMany(r.Context(), keys); err != nil {
		a.log().Warn("business blob purge failed", "business", id, "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- messages ----

type messageJSON struct {
	ID              uuid.UUID  `json:"id"`
	BusinessID      *uuid.UUID `json:"business_id"`
	Direction       string     `json:"direction"`
	FromAddr        *string    `json:"from_addr"`
	FromName        *string    `json:"from_name"`
	Subject         *string    `json:"subject"`
	Snippet         *string    `json:"snippet"`
	ToLocal         *string    `json:"to_local"`
	ToTag           *string    `json:"to_tag"`
	ReceivedAt      time.Time  `json:"received_at"`
	IsRead          bool       `json:"is_read"`
	Archived        bool       `json:"archived"`
	AttachmentCount int        `json:"attachment_count"`
	ThreadKey       string     `json:"thread_key"`
	Route           string     `json:"route"`
}

func toMessage(m store.Message) messageJSON {
	return messageJSON{ID: m.ID, BusinessID: m.BusinessID, Direction: m.Direction, FromAddr: m.FromAddr, FromName: m.FromName,
		Subject: m.Subject, Snippet: m.Snippet, ToLocal: m.ToLocal, ToTag: m.ToTag, ReceivedAt: m.ReceivedAt, IsRead: m.IsRead,
		Archived: m.ArchivedAt != nil, AttachmentCount: m.AttachmentCount, ThreadKey: m.ThreadKey, Route: m.Route}
}

func toMessages(ms []store.Message) []messageJSON {
	out := make([]messageJSON, 0, len(ms))
	for _, m := range ms {
		out = append(out, toMessage(m))
	}
	return out
}

func encodeCursor(c store.Cursor) string { return store.EncodeCursor(c) }

func decodeCursor(s string) (*store.Cursor, error) { return store.DecodeCursor(s) }

func (a *API) listMessages(w http.ResponseWriter, r *http.Request) {
	qv := r.URL.Query()
	p := store.ListMessagesParams{ToLocal: strings.ToLower(qv.Get("to")), Limit: 50}
	if v := qv.Get("business"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			bad(w, "invalid_business", "business must be a uuid")
			return
		}
		p.BusinessID = &id
	}
	if v := qv.Get("unread"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			bad(w, "invalid_unread", "unread must be true or false")
			return
		}
		p.UnreadOnly = b
	}
	if v := qv.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			bad(w, "invalid_limit", "limit must be 1..200")
			return
		}
		p.Limit = n
	}
	if v := qv.Get("cursor"); v != "" {
		c, err := decodeCursor(v)
		if err != nil {
			bad(w, "invalid_cursor", "")
			return
		}
		p.Cursor = c
	}
	limit := p.Limit
	p.Limit = limit + 1
	ms, err := a.Store.ListMessages(r.Context(), p)
	if err != nil {
		a.internal(w, err)
		return
	}
	next := ""
	if len(ms) > limit {
		ms = ms[:limit]
		next = encodeCursor(store.Cursor{ReceivedAt: ms[limit-1].ReceivedAt, ID: ms[limit-1].ID})
	}
	var nc any
	if next != "" {
		nc = next
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"messages": toMessages(ms), "next_cursor": nc})
}

type attachmentJSON struct {
	ID        uuid.UUID `json:"id"`
	Filename  *string   `json:"filename"`
	Mime      *string   `json:"mime"`
	SizeBytes *int      `json:"size_bytes"`
	ContentID *string   `json:"content_id"`
	Inline    bool      `json:"inline"`
}

func (a *API) getMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	m, err := a.Store.GetMessage(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		a.internal(w, err)
		return
	}
	thread, err := a.Store.ListThread(ctx, m.ThreadKey)
	if err != nil {
		a.internal(w, err)
		return
	}
	atts, err := a.Store.ListAttachments(ctx, id)
	if err != nil {
		a.internal(w, err)
		return
	}
	d, err := a.Store.GetMessageDetails(ctx, id)
	if err != nil {
		a.internal(w, err)
		return
	}
	aj := make([]attachmentJSON, 0, len(atts))
	for _, x := range atts {
		aj = append(aj, attachmentJSON{x.ID, x.Filename, x.Mime, x.SizeBytes, x.ContentID, x.Inline})
	}
	labels := []string{}
	if d.GmailLabel != nil && *d.GmailLabel != "" {
		labels = append(labels, *d.GmailLabel)
	}
	var biz any
	if m.BusinessID != nil && d.BusinessName != nil {
		biz = map[string]any{"id": m.BusinessID, "name": d.BusinessName, "domain": d.BusinessDomain}
	}
	var gmail any
	if d.GmailState != nil {
		gmail = map[string]any{"state": d.GmailState, "last_error": d.GmailError, "attempts": d.GmailAttempts,
			"last_attempt_at": d.GmailLastTry, "last_synced_at": d.GmailLastSync}
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"message":     toMessage(m),
		"thread":      toMessages(thread),
		"attachments": aj,
		"details": map[string]any{
			"business":       biz,
			"address":        m.EnvTo,
			"rule_name":      d.RuleName,
			"gmail":          gmail,
			"gmail_id":       d.GmailID,
			"failure_reason": d.FailureReason,
			"labels":         labels,
			"size_bytes":     m.SizeBytes,
			"received_at":    m.ReceivedAt,
		},
	})
}

var remoteImgRE = regexp.MustCompile(`(?i)<img\b[^>]*\bsrc\s*=\s*["']?\s*(https?:)?//`)

func (a *API) getBody(w http.ResponseWriter, r *http.Request) {
	if !a.blobOK(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	m, err := a.Store.GetMessage(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		a.internal(w, err)
		return
	}
	if m.RawKey == nil {
		httpapi.WriteProblem(w, http.StatusNotFound, "raw_unavailable", "no stored copy of this message")
		return
	}
	rc, err := a.Blob.Get(ctx, *m.RawKey)
	if err != nil {
		httpapi.WriteProblem(w, http.StatusNotFound, "raw_unavailable", "stored copy is missing")
		return
	}
	raw, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		a.internal(w, err)
		return
	}
	p, err := parse.Parse(raw)
	if err != nil {
		httpapi.WriteProblem(w, http.StatusUnprocessableEntity, "parse_failed", err.Error())
		return
	}
	atts, err := a.Store.ListAttachments(ctx, id)
	if err != nil {
		a.internal(w, err)
		return
	}
	type inlineJSON struct {
		CID          string     `json:"cid"`
		AttachmentID *uuid.UUID `json:"attachment_id"`
		Mime         string     `json:"mime"`
	}
	inline := []inlineJSON{}
	for _, x := range p.Attachments {
		if x.ContentID == "" {
			continue
		}
		it := inlineJSON{CID: x.ContentID, Mime: x.MIME}
		for _, s := range atts {
			if s.ContentID != nil && *s.ContentID == x.ContentID {
				sid := s.ID
				it.AttachmentID = &sid
			}
		}
		inline = append(inline, it)
	}
	var html, text any
	if p.HTML != "" {
		html = p.HTML
	}
	if p.Text != "" {
		text = p.Text
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"html":               html,
		"text":               text,
		"remote_image_count": len(remoteImgRE.FindAllStringIndex(p.HTML, -1)),
		"auth_results":       map[string]string{"spf": p.Auth.SPF, "dkim": p.Auth.DKIM, "dmarc": p.Auth.DMARC},
		"inline":             inline,
	})
}

func (a *API) getRaw(w http.ResponseWriter, r *http.Request) {
	if !a.blobOK(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	m, err := a.Store.GetMessage(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		a.internal(w, err)
		return
	}
	if m.RawKey == nil {
		httpapi.WriteProblem(w, http.StatusNotFound, "raw_unavailable", "no stored copy of this message")
		return
	}
	rc, err := a.Blob.Get(r.Context(), *m.RawKey)
	if err != nil {
		httpapi.WriteProblem(w, http.StatusNotFound, "raw_unavailable", "stored copy is missing")
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "message/rfc822")
	w.Header().Set("Content-Disposition", `attachment; filename="`+id.String()+`.eml"`)
	if m.SizeBytes != nil {
		w.Header().Set("Content-Length", strconv.Itoa(*m.SizeBytes))
	}
	_, _ = io.Copy(w, rc)
}

func (a *API) getAttachment(w http.ResponseWriter, r *http.Request) {
	if !a.blobOK(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	at, err := a.Store.GetAttachment(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		a.internal(w, err)
		return
	}
	u, err := a.Blob.PresignGet(r.Context(), at.R2Key, attachmentTTL)
	if err != nil {
		a.internal(w, err)
		return
	}
	http.Redirect(w, r, u, http.StatusFound)
}

func (a *API) batch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []uuid.UUID `json:"ids"`
		Op  string      `json:"op"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > maxBatch {
		bad(w, "invalid_ids", "ids must contain 1..200 entries")
		return
	}
	switch in.Op {
	case "read", "unread", "archive", "unarchive":
	default:
		bad(w, "invalid_op", "op must be read, unread, archive or unarchive")
		return
	}
	err := a.Store.WithTx(r.Context(), func(q *store.Queries) error {
		ctx := r.Context()
		switch in.Op {
		case "read", "unread":
			_, err := q.SetMessagesRead(ctx, in.IDs, in.Op == "read")
			return err
		default:
			arch := in.Op == "archive"
			if _, err := q.SetMessagesArchived(ctx, in.IDs, arch); err != nil {
				return err
			}
			// Hook for the write-back engine (T19): it picks these rows up once the undo window passes.
			return q.WantGmailArchived(ctx, in.IDs, arch, archiveUndoWindow)
		}
	})
	if err != nil {
		a.internal(w, err)
		return
	}
	ms, err := a.Store.GetMessages(r.Context(), in.IDs)
	if err != nil {
		a.internal(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"messages": toMessages(ms)})
}
