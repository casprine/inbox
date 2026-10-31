package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Message struct {
	ID              uuid.UUID
	Seq             int64
	BusinessID      *uuid.UUID
	Direction       string // in | out
	Source          string // edge | backfill | gmail_thread
	State           string // stored | failed
	FailureReason   *string
	DismissedAt     *time.Time
	MessageIDHdr    string
	EnvTo           *string
	EnvToAll        []string
	ToLocal         *string
	ToTag           *string
	FromAddr        *string
	FromName        *string
	Subject         *string
	Snippet         *string
	InReplyTo       *string
	ThreadKey       string
	ReceivedAt      time.Time
	SizeBytes       *int
	AttachmentCount int
	RawKey          *string
	AuthResults     json.RawMessage
	Route           string // pending | routed | unrouted
	RuleID          *uuid.UUID
	IsRead          bool
	ReadAt          *time.Time
	ArchivedAt      *time.Time
	GmailID         *string
	GmailThreadID   *string
	CreatedAt       time.Time
}

const messageCols = `id, seq, business_id, direction::text, source::text, state::text, failure_reason, dismissed_at,
message_id_hdr, env_to, env_to_all, to_local, to_tag, from_addr, from_name, subject, snippet, in_reply_to,
thread_key, received_at, size_bytes, attachment_count, raw_key, auth_results, route::text, rule_id, is_read,
read_at, archived_at, gmail_id, gmail_thread_id, created_at`

// messageColsM is messageCols qualified with the alias m, for joins.
const messageColsM = `m.id, m.seq, m.business_id, m.direction::text, m.source::text, m.state::text, m.failure_reason, m.dismissed_at,
m.message_id_hdr, m.env_to, m.env_to_all, m.to_local, m.to_tag, m.from_addr, m.from_name, m.subject, m.snippet, m.in_reply_to,
m.thread_key, m.received_at, m.size_bytes, m.attachment_count, m.raw_key, m.auth_results, m.route::text, m.rule_id, m.is_read,
m.read_at, m.archived_at, m.gmail_id, m.gmail_thread_id, m.created_at`

func scanMessage(row pgx.Row) (Message, error) {
	var m Message
	err := row.Scan(&m.ID, &m.Seq, &m.BusinessID, &m.Direction, &m.Source, &m.State, &m.FailureReason, &m.DismissedAt,
		&m.MessageIDHdr, &m.EnvTo, &m.EnvToAll, &m.ToLocal, &m.ToTag, &m.FromAddr, &m.FromName, &m.Subject, &m.Snippet,
		&m.InReplyTo, &m.ThreadKey, &m.ReceivedAt, &m.SizeBytes, &m.AttachmentCount, &m.RawKey, &m.AuthResults,
		&m.Route, &m.RuleID, &m.IsRead, &m.ReadAt, &m.ArchivedAt, &m.GmailID, &m.GmailThreadID, &m.CreatedAt)
	return m, err
}

// InsertMessage inserts m; on a duplicate message_id_hdr it does nothing and
// returns inserted=false (the existing row is not modified).
func (q *Queries) InsertMessage(ctx context.Context, m Message) (inserted bool, err error) {
	if m.Direction == "" {
		m.Direction = "in"
	}
	if m.State == "" {
		m.State = "stored"
	}
	if m.Route == "" {
		m.Route = "pending"
	}
	if m.EnvToAll == nil {
		m.EnvToAll = []string{}
	}
	var auth any
	if len(m.AuthResults) > 0 {
		auth = []byte(m.AuthResults)
	}
	var id uuid.UUID
	err = q.db.QueryRow(ctx, `
INSERT INTO messages (id, business_id, direction, source, state, failure_reason, message_id_hdr, env_to, env_to_all,
  to_local, to_tag, from_addr, from_name, subject, snippet, in_reply_to, thread_key, received_at, size_bytes,
  attachment_count, raw_key, auth_results, route, rule_id, is_read, gmail_id, gmail_thread_id)
VALUES ($1,$2,$3::msg_dir,$4::msg_source,$5::msg_state,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,
  $23::route_state,$24,$25,$26,$27)
ON CONFLICT (message_id_hdr) DO NOTHING RETURNING id`,
		m.ID, m.BusinessID, m.Direction, m.Source, m.State, m.FailureReason, m.MessageIDHdr, m.EnvTo, m.EnvToAll,
		m.ToLocal, m.ToTag, m.FromAddr, m.FromName, m.Subject, m.Snippet, m.InReplyTo, m.ThreadKey, m.ReceivedAt,
		m.SizeBytes, m.AttachmentCount, m.RawKey, auth, m.Route, m.RuleID, m.IsRead, m.GmailID, m.GmailThreadID).Scan(&id)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (q *Queries) GetMessage(ctx context.Context, id uuid.UUID) (Message, error) {
	m, err := scanMessage(q.db.QueryRow(ctx, `SELECT `+messageCols+` FROM messages WHERE id=$1`, id))
	return m, notFound(err)
}

func (q *Queries) GetMessageByHeaderID(ctx context.Context, hdr string) (Message, error) {
	m, err := scanMessage(q.db.QueryRow(ctx, `SELECT `+messageCols+` FROM messages WHERE message_id_hdr=$1`, hdr))
	return m, notFound(err)
}

func (q *Queries) GetMessageByRawKey(ctx context.Context, key string) (Message, error) {
	m, err := scanMessage(q.db.QueryRow(ctx, `SELECT `+messageCols+` FROM messages WHERE raw_key=$1 LIMIT 1`, key))
	return m, notFound(err)
}

// Cursor is the keyset position of the last row of a page.
type Cursor struct {
	ReceivedAt time.Time
	ID         uuid.UUID
}

// EncodeCursor / DecodeCursor turn a (received_at, id) keyset position into the opaque string clients send back.
func EncodeCursor(c Cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.ReceivedAt.UnixMicro(), 10) + "|" + c.ID.String()))
}

func DecodeCursor(s string) (*Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	us, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return nil, errors.New("malformed cursor")
	}
	n, err := strconv.ParseInt(us, 10, 64)
	if err != nil {
		return nil, err
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	return &Cursor{ReceivedAt: time.UnixMicro(n).UTC(), ID: u}, nil
}

type ListMessagesParams struct {
	BusinessID *uuid.UUID
	ToLocal    string
	UnreadOnly bool
	Cursor     *Cursor
	Limit      int // default 50
}

// ListMessages returns non-archived stored messages newest first. The caller
// detects another page by requesting Limit+1 or by len(result)==Limit.
func (q *Queries) ListMessages(ctx context.Context, p ListMessagesParams) ([]Message, error) {
	if p.Limit <= 0 {
		p.Limit = 50
	}
	var curAt *time.Time
	var curID *uuid.UUID
	if p.Cursor != nil {
		curAt, curID = &p.Cursor.ReceivedAt, &p.Cursor.ID
	}
	rows, err := q.db.Query(ctx, `
SELECT `+messageCols+` FROM messages
WHERE archived_at IS NULL AND state='stored' AND direction='in'
  AND ($1::uuid IS NULL OR business_id=$1)
  AND ($2::text = '' OR to_local=$2)
  AND (NOT $3 OR NOT is_read)
  AND ($4::timestamptz IS NULL OR (received_at, id) < ($4, $5::uuid))
ORDER BY received_at DESC, id DESC LIMIT $6`,
		p.BusinessID, p.ToLocal, p.UnreadOnly, curAt, curID, p.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListThread returns all stored messages sharing thread_key, oldest first.
func (q *Queries) ListThread(ctx context.Context, threadKey string) ([]Message, error) {
	rows, err := q.db.Query(ctx, `SELECT `+messageCols+` FROM messages WHERE thread_key=$1 AND state='stored' ORDER BY received_at, id`, threadKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetMessagesRead marks ids read/unread; returns rows changed.
func (q *Queries) SetMessagesRead(ctx context.Context, ids []uuid.UUID, read bool) (int64, error) {
	tag, err := q.db.Exec(ctx, `
UPDATE messages SET is_read=$2, read_at = CASE WHEN $2 THEN now() ELSE NULL END
WHERE id = ANY($1) AND is_read <> $2`, ids, read)
	return tag.RowsAffected(), err
}

// SetMessagesArchived archives or unarchives ids; returns rows changed.
func (q *Queries) SetMessagesArchived(ctx context.Context, ids []uuid.UUID, archived bool) (int64, error) {
	tag, err := q.db.Exec(ctx, `
UPDATE messages SET archived_at = CASE WHEN $2 THEN now() ELSE NULL END
WHERE id = ANY($1) AND (archived_at IS NOT NULL) <> $2`, ids, archived)
	return tag.RowsAffected(), err
}

// SetMessageRoute records routing outcome.
func (q *Queries) SetMessageRoute(ctx context.Context, id uuid.UUID, businessID *uuid.UUID, route string, ruleID *uuid.UUID) error {
	_, err := q.db.Exec(ctx, `UPDATE messages SET business_id=$2, route=$3::route_state, rule_id=$4 WHERE id=$1`, id, businessID, route, ruleID)
	return err
}

// SetMessageDismissed sets or clears dismissed_at on a failed message.
func (q *Queries) SetMessageDismissed(ctx context.Context, id uuid.UUID, dismissed bool) error {
	_, err := q.db.Exec(ctx, `UPDATE messages SET dismissed_at = CASE WHEN $2 THEN now() ELSE NULL END WHERE id=$1`, id, dismissed)
	return err
}

// FindThreadKey looks up the thread_key of the first existing message whose
// Message-ID is among hdrs (In-Reply-To / References), if any.
func (q *Queries) FindThreadKey(ctx context.Context, hdrs []string) (string, bool, error) {
	var key string
	err := q.db.QueryRow(ctx, `SELECT thread_key FROM messages WHERE message_id_hdr = ANY($1) ORDER BY received_at LIMIT 1`, hdrs).Scan(&key)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	return key, err == nil, err
}

type Attachment struct {
	ID        uuid.UUID
	MessageID uuid.UUID
	Filename  *string
	Mime      *string
	SizeBytes *int
	ContentID *string
	Inline    bool
	R2Key     string
}

func (q *Queries) InsertAttachment(ctx context.Context, a Attachment) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO attachments (id, message_id, filename, mime, size_bytes, content_id, inline, r2_key)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, a.ID, a.MessageID, a.Filename, a.Mime, a.SizeBytes, a.ContentID, a.Inline, a.R2Key)
	return err
}

func (q *Queries) GetAttachment(ctx context.Context, id uuid.UUID) (Attachment, error) {
	var a Attachment
	err := q.db.QueryRow(ctx, `SELECT id, message_id, filename, mime, size_bytes, content_id, inline, r2_key FROM attachments WHERE id=$1`, id).
		Scan(&a.ID, &a.MessageID, &a.Filename, &a.Mime, &a.SizeBytes, &a.ContentID, &a.Inline, &a.R2Key)
	return a, notFound(err)
}

func (q *Queries) ListAttachments(ctx context.Context, messageID uuid.UUID) ([]Attachment, error) {
	rows, err := q.db.Query(ctx, `SELECT id, message_id, filename, mime, size_bytes, content_id, inline, r2_key FROM attachments WHERE message_id=$1 ORDER BY filename, id`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.MessageID, &a.Filename, &a.Mime, &a.SizeBytes, &a.ContentID, &a.Inline, &a.R2Key); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpsertMessageSearch writes the search row. bodyText is stored as given (callers cap it at 200 KB);
// fts weights: subject A, from B, body C, filenames D.
func (q *Queries) UpsertMessageSearch(ctx context.Context, messageID uuid.UUID, subject, from, bodyText, filenames string) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO message_search (message_id, body_text, fts) VALUES ($1, $4,
  setweight(to_tsvector('simple', $2), 'A') || setweight(to_tsvector('simple', $3), 'B') ||
  setweight(to_tsvector('simple', $4), 'C') || setweight(to_tsvector('simple', $5), 'D'))
ON CONFLICT (message_id) DO UPDATE SET body_text=EXCLUDED.body_text, fts=EXCLUDED.fts`,
		messageID, subject, from, bodyText, filenames)
	return err
}

// SearchMessageIDs is a minimal FTS lookup (T09 builds the operator parser on top).
func (q *Queries) SearchMessageIDs(ctx context.Context, tsquery string, limit int) ([]uuid.UUID, error) {
	rows, err := q.db.Query(ctx, `
SELECT message_id FROM message_search WHERE fts @@ websearch_to_tsquery('simple', $1)
ORDER BY ts_rank(fts, websearch_to_tsquery('simple', $1)) DESC LIMIT $2`, tsquery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

type IngestLogLine struct {
	ID        int64
	IngestKey string
	MessageID *uuid.UUID
	At        time.Time
	Level     string
	Line      string
}

func (q *Queries) AppendIngestLog(ctx context.Context, ingestKey string, messageID *uuid.UUID, level, line string) error {
	_, err := q.db.Exec(ctx, `INSERT INTO ingest_log (ingest_key, message_id, level, line) VALUES ($1,$2,$3,$4)`, ingestKey, messageID, level, line)
	return err
}

// ListIngestLog returns lines for a key (or all lines of a message), oldest first.
func (q *Queries) ListIngestLog(ctx context.Context, ingestKey string) ([]IngestLogLine, error) {
	rows, err := q.db.Query(ctx, `SELECT id, ingest_key, message_id, at, level, line FROM ingest_log WHERE ingest_key=$1 ORDER BY id`, ingestKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IngestLogLine
	for rows.Next() {
		var l IngestLogLine
		if err := rows.Scan(&l.ID, &l.IngestKey, &l.MessageID, &l.At, &l.Level, &l.Line); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
