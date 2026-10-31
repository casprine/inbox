package store

import (
	"context"

	"github.com/google/uuid"
)

// UnroutedCount is the number of visible unrouted messages for one business (nil = no business).
type UnroutedCount struct {
	BusinessID *uuid.UUID
	Count      int
}

// UnroutedCounts counts stored, non-archived, unrouted messages per business.
func (q *Queries) UnroutedCounts(ctx context.Context) ([]UnroutedCount, error) {
	rows, err := q.db.Query(ctx, `
SELECT m.business_id, count(*) FROM messages m
WHERE m.route='unrouted' AND m.state='stored' AND m.direction='in' AND m.archived_at IS NULL
GROUP BY m.business_id ORDER BY count(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnroutedCount
	for rows.Next() {
		var u UnroutedCount
		if err := rows.Scan(&u.BusinessID, &u.Count); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountFailed counts failed, undismissed messages.
func (q *Queries) CountFailed(ctx context.Context) (int, error) {
	var n int
	err := q.db.QueryRow(ctx, `SELECT count(*) FROM messages WHERE state='failed' AND dismissed_at IS NULL`).Scan(&n)
	return n, err
}

// ListUnrouted returns stored, non-archived, unrouted messages newest first.
func (q *Queries) ListUnrouted(ctx context.Context, limit int) ([]Message, error) {
	return q.listMsgs(ctx, `SELECT `+messageCols+` FROM messages
WHERE route='unrouted' AND state='stored' AND direction='in' AND archived_at IS NULL ORDER BY received_at DESC, id DESC LIMIT $1`, limit)
}

// ListFailed returns failed, undismissed messages newest first.
func (q *Queries) ListFailed(ctx context.Context, limit int) ([]Message, error) {
	return q.listMsgs(ctx, `SELECT `+messageCols+` FROM messages
WHERE state='failed' AND dismissed_at IS NULL ORDER BY received_at DESC, id DESC LIMIT $1`, limit)
}

func (q *Queries) listMsgs(ctx context.Context, sql string, args ...any) ([]Message, error) {
	rows, err := q.db.Query(ctx, sql, args...)
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

// DeleteFailedMessage removes a failed, undismissed message row (retry re-creates it from the raw blob).
func (q *Queries) DeleteFailedMessage(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := q.db.Exec(ctx, `DELETE FROM messages WHERE id=$1 AND state='failed'`, id)
	return tag.RowsAffected() == 1, err
}

// BusinessRef is the minimal business identity used when grouping results.
type BusinessRef struct {
	ID     uuid.UUID
	Name   string
	Domain string
	Sort   int
}

// ListBusinessRefs returns all businesses in display order.
func (q *Queries) ListBusinessRefs(ctx context.Context) ([]BusinessRef, error) {
	rows, err := q.db.Query(ctx, `SELECT id, name, domain, sort_order FROM businesses ORDER BY sort_order, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BusinessRef
	for rows.Next() {
		var b BusinessRef
		if err := rows.Scan(&b.ID, &b.Name, &b.Domain, &b.Sort); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
