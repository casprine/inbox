package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// alfatherFeed is which messages Alfather is offered: stored inbound mail that arrived live.
// Backfilled history is excluded so importing old mail never floods the "waiting" count.
const alfatherFeed = `m.direction='in' AND m.state='stored' AND m.source <> 'backfill'`

// Consumer is a paired reader of the mail feed.
type Consumer struct {
	ID         uuid.UUID
	CursorSeq  int64
	LastSeenAt *time.Time
}

// MaxSeq is the highest message sequence number (0 when there are none).
func (q *Queries) MaxSeq(ctx context.Context) (int64, error) {
	var n int64
	err := q.db.QueryRow(ctx, `SELECT COALESCE(max(seq), 0) FROM messages`).Scan(&n)
	return n, err
}

// ReplaceAlfather removes any paired Alfather and creates a new consumer starting at cursor.
func (s *Store) ReplaceAlfather(ctx context.Context, id uuid.UUID, tokenHash []byte, includeExisting bool) error {
	return s.WithTx(ctx, func(q *Queries) error {
		if _, err := q.db.Exec(ctx, `DELETE FROM consumers WHERE kind='alfather'`); err != nil {
			return err
		}
		cursor := int64(0)
		if !includeExisting {
			var err error
			if cursor, err = q.MaxSeq(ctx); err != nil {
				return err
			}
		}
		_, err := q.db.Exec(ctx, `INSERT INTO consumers (id, kind, token_hash, cursor_seq) VALUES ($1,'alfather',$2,$3)`, id, tokenHash, cursor)
		return err
	})
}

// DeleteAlfather unpairs; false when nothing was paired.
func (q *Queries) DeleteAlfather(ctx context.Context) (bool, error) {
	tag, err := q.db.Exec(ctx, `DELETE FROM consumers WHERE kind='alfather'`)
	return tag.RowsAffected() > 0, err
}

// AlfatherByTokenHash finds the consumer owning a token and marks it seen.
func (q *Queries) AlfatherByTokenHash(ctx context.Context, hash []byte) (Consumer, error) {
	var c Consumer
	err := q.db.QueryRow(ctx, `UPDATE consumers SET last_seen_at=now() WHERE kind='alfather' AND token_hash=$1
RETURNING id, cursor_seq, last_seen_at`, hash).Scan(&c.ID, &c.CursorSeq, &c.LastSeenAt)
	return c, notFound(err)
}

// AdvanceCursor moves the consumer's cursor forward (never back) and returns the new value.
func (q *Queries) AdvanceCursor(ctx context.Context, id uuid.UUID, upTo int64) (int64, error) {
	var c int64
	err := q.db.QueryRow(ctx, `UPDATE consumers SET cursor_seq = GREATEST(cursor_seq, $2) WHERE id=$1 RETURNING cursor_seq`, id, upTo).Scan(&c)
	return c, notFound(err)
}

// CountAfter counts feed messages after seq.
func (q *Queries) CountAfter(ctx context.Context, seq int64) (int, error) {
	var n int
	err := q.db.QueryRow(ctx, `SELECT count(*) FROM messages m WHERE m.seq > $1 AND `+alfatherFeed, seq).Scan(&n)
	return n, err
}

// FeedMessage is a message with what Alfather needs to read it.
type FeedMessage struct {
	Message
	BusinessName   *string
	BusinessDomain *string
	RuleName       *string
	Body           *string
}

const feedSelect = `SELECT ` + messageColsM + `, b.name, b.domain, r.name, s.body_text
FROM messages m
LEFT JOIN businesses b ON b.id = m.business_id
LEFT JOIN rules r ON r.id = m.rule_id
LEFT JOIN message_search s ON s.message_id = m.id`

func scanFeed(row interface{ Scan(...any) error }) (FeedMessage, error) {
	var f FeedMessage
	m := &f.Message
	err := row.Scan(&m.ID, &m.Seq, &m.BusinessID, &m.Direction, &m.Source, &m.State, &m.FailureReason, &m.DismissedAt,
		&m.MessageIDHdr, &m.EnvTo, &m.EnvToAll, &m.ToLocal, &m.ToTag, &m.FromAddr, &m.FromName, &m.Subject, &m.Snippet,
		&m.InReplyTo, &m.ThreadKey, &m.ReceivedAt, &m.SizeBytes, &m.AttachmentCount, &m.RawKey, &m.AuthResults,
		&m.Route, &m.RuleID, &m.IsRead, &m.ReadAt, &m.ArchivedAt, &m.GmailID, &m.GmailThreadID, &m.CreatedAt,
		&f.BusinessName, &f.BusinessDomain, &f.RuleName, &f.Body)
	return f, err
}

// FeedAfter returns up to limit feed messages with seq > after, oldest first, and whether more follow.
func (q *Queries) FeedAfter(ctx context.Context, after int64, limit int) ([]FeedMessage, bool, error) {
	rows, err := q.db.Query(ctx, feedSelect+` WHERE m.seq > $1 AND `+alfatherFeed+` ORDER BY m.seq LIMIT $2`, after, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []FeedMessage
	for rows.Next() {
		f, err := scanFeed(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}

// FeedByID returns one stored message with its body, any direction or source.
func (q *Queries) FeedByID(ctx context.Context, id uuid.UUID) (FeedMessage, error) {
	f, err := scanFeed(q.db.QueryRow(ctx, feedSelect+` WHERE m.id=$1 AND m.state='stored'`, id))
	return f, notFound(err)
}

// FeedByIDs fills in business, rule and body for the given message ids (same order not guaranteed).
func (q *Queries) FeedByIDs(ctx context.Context, ids []uuid.UUID) ([]FeedMessage, error) {
	rows, err := q.db.Query(ctx, feedSelect+` WHERE m.id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedMessage
	for rows.Next() {
		f, err := scanFeed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// AttachmentsFor lists attachments (non-inline) of the given messages, keyed by message id.
func (q *Queries) AttachmentsFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]Attachment, error) {
	rows, err := q.db.Query(ctx, `SELECT id, message_id, filename, mime, size_bytes, content_id, inline, r2_key
FROM attachments WHERE message_id = ANY($1) AND NOT inline ORDER BY message_id, id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID][]Attachment{}
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.MessageID, &a.Filename, &a.Mime, &a.SizeBytes, &a.ContentID, &a.Inline, &a.R2Key); err != nil {
			return nil, err
		}
		out[a.MessageID] = append(out[a.MessageID], a)
	}
	return out, rows.Err()
}
