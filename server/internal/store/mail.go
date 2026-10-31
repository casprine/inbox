package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ListAllBusinessAddresses returns every address seen, grouped by caller.
func (q *Queries) ListAllBusinessAddresses(ctx context.Context) ([]BusinessAddress, error) {
	rows, err := q.db.Query(ctx, `SELECT business_id, local_part, msg_count, last_at FROM business_addresses ORDER BY msg_count DESC, local_part`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BusinessAddress
	for rows.Next() {
		var a BusinessAddress
		if err := rows.Scan(&a.BusinessID, &a.LocalPart, &a.MsgCount, &a.LastAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// NextBusinessSortOrder returns max(sort_order)+1.
func (q *Queries) NextBusinessSortOrder(ctx context.Context) (int, error) {
	var n int
	err := q.db.QueryRow(ctx, `SELECT COALESCE(max(sort_order)+1, 0) FROM businesses`).Scan(&n)
	return n, err
}

// BusinessBlobKeys lists the raw and attachment object keys of every message of a business.
func (q *Queries) BusinessBlobKeys(ctx context.Context, businessID uuid.UUID) ([]string, error) {
	rows, err := q.db.Query(ctx, `
SELECT raw_key FROM messages WHERE business_id=$1 AND raw_key IS NOT NULL
UNION
SELECT a.r2_key FROM attachments a JOIN messages m ON m.id=a.message_id WHERE m.business_id=$1`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteBusinessMessages removes all messages of a business (attachments, search, sync cascade).
func (q *Queries) DeleteBusinessMessages(ctx context.Context, businessID uuid.UUID) error {
	_, err := q.db.Exec(ctx, `DELETE FROM messages WHERE business_id=$1`, businessID)
	return err
}

// GetMessages returns the messages with the given ids (any order).
func (q *Queries) GetMessages(ctx context.Context, ids []uuid.UUID) ([]Message, error) {
	rows, err := q.db.Query(ctx, `SELECT `+messageCols+` FROM messages WHERE id = ANY($1) ORDER BY received_at DESC, id DESC`, ids)
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

// MessageDetails are the joined facts shown on the Details sheet.
type MessageDetails struct {
	BusinessName   *string
	BusinessDomain *string
	RuleName       *string
	GmailState     *string
	GmailLabel     *string
	GmailError     *string
	GmailID        *string
	GmailAttempts  *int
	GmailLastTry   *time.Time
	GmailLastSync  *time.Time
	FailureReason  *string
}

func (q *Queries) GetMessageDetails(ctx context.Context, id uuid.UUID) (MessageDetails, error) {
	var d MessageDetails
	err := q.db.QueryRow(ctx, `
SELECT b.name, b.domain, r.name, g.state::text, g.want_label, g.last_error,
       m.gmail_id, g.attempts, g.last_attempt_at, g.last_synced_at, m.failure_reason
FROM messages m
LEFT JOIN businesses b ON b.id = m.business_id
LEFT JOIN rules r ON r.id = m.rule_id
LEFT JOIN gmail_sync g ON g.message_id = m.id
WHERE m.id=$1`, id).Scan(&d.BusinessName, &d.BusinessDomain, &d.RuleName, &d.GmailState, &d.GmailLabel, &d.GmailError,
		&d.GmailID, &d.GmailAttempts, &d.GmailLastTry, &d.GmailLastSync, &d.FailureReason)
	return d, notFound(err)
}

// WantGmailArchived upserts gmail_sync so the write-back engine (T19) converges Gmail to
// the given archive state after delay (the undo window).
func (q *Queries) WantGmailArchived(ctx context.Context, ids []uuid.UUID, archived bool, delay time.Duration) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO gmail_sync (message_id, want_archived, state, attempts, next_attempt_at)
SELECT id, $2, 'pending', 0, now() + make_interval(secs => $3) FROM messages WHERE id = ANY($1)
ON CONFLICT (message_id) DO UPDATE
SET want_archived = EXCLUDED.want_archived, state='pending', attempts=0,
    next_attempt_at = EXCLUDED.next_attempt_at, updated_at = now()`, ids, archived, delay.Seconds())
	return err
}
