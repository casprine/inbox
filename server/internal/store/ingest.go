package store

import (
	"context"

	"github.com/google/uuid"
)

// NotifyMailNew publishes on the mail_new channel (consumed by the SSE hub). Inside a
// transaction the notification is delivered on commit.
func (q *Queries) NotifyMailNew(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, `SELECT pg_notify('mail_new', $1)`, id.String())
	return err
}

// RawKeyKnown reports whether a raw blob key has already been ingested (a message
// holds it, or ingest_log has lines for it).
func (q *Queries) RawKeyKnown(ctx context.Context, key string) (bool, error) {
	var ok bool
	err := q.db.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM messages WHERE raw_key=$1) OR EXISTS (SELECT 1 FROM ingest_log WHERE ingest_key=$1)`, key).Scan(&ok)
	return ok, err
}

// RawKeysKnown is RawKeyKnown for many keys at once: the returned set holds those already ingested.
func (q *Queries) RawKeysKnown(ctx context.Context, keys []string) (map[string]bool, error) {
	out := make(map[string]bool, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := q.db.Query(ctx, `
SELECT k FROM unnest($1::text[]) AS k
WHERE EXISTS (SELECT 1 FROM messages WHERE raw_key=k) OR EXISTS (SELECT 1 FROM ingest_log WHERE ingest_key=k)`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}
