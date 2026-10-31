package store

import (
	"context"
	"encoding/json"
)

// GetSetting returns the raw JSON value for key, or ErrNotFound.
func (q *Queries) GetSetting(ctx context.Context, key string) (json.RawMessage, error) {
	var v []byte
	err := q.db.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&v)
	return v, notFound(err)
}

// SetSetting upserts key to the JSON encoding of value.
func (q *Queries) SetSetting(ctx context.Context, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = q.db.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1,$2) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`, key, b)
	return err
}

// DeleteExpiredIdempotencyKeys supports the T27 prune job.
func (q *Queries) DeleteExpiredIdempotencyKeys(ctx context.Context, olderThanHours int) (int64, error) {
	tag, err := q.db.Exec(ctx, `DELETE FROM idempotency_keys WHERE at < now() - make_interval(hours => $1)`, olderThanHours)
	return tag.RowsAffected(), err
}

// DeleteSetting removes key (missing is not an error).
func (q *Queries) DeleteSetting(ctx context.Context, key string) error {
	_, err := q.db.Exec(ctx, `DELETE FROM settings WHERE key=$1`, key)
	return err
}
