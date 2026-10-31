package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PruneMessages deletes up to limit messages (all of them when cutoff is nil, else those received
// before it) and returns how many, plus the raw and attachment blob keys to delete afterwards.
// Rows go first: a failed blob delete leaves harmless orphans, never rows pointing at nothing.
func (s *Store) PruneMessages(ctx context.Context, cutoff *time.Time, limit int) (int, []string, error) {
	var ids []uuid.UUID
	var keys []string
	err := s.WithTx(ctx, func(q *Queries) error {
		rows, err := q.db.Query(ctx, `SELECT id FROM messages WHERE ($1::timestamptz IS NULL OR received_at < $1)
ORDER BY received_at LIMIT $2 FOR UPDATE SKIP LOCKED`, cutoff, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil || len(ids) == 0 {
			return err
		}
		krows, err := q.db.Query(ctx, `
SELECT raw_key FROM messages WHERE id = ANY($1) AND raw_key IS NOT NULL
UNION
SELECT r2_key FROM attachments WHERE message_id = ANY($1)`, ids)
		if err != nil {
			return err
		}
		for krows.Next() {
			var k string
			if err := krows.Scan(&k); err != nil {
				krows.Close()
				return err
			}
			keys = append(keys, k)
		}
		krows.Close()
		if err := krows.Err(); err != nil {
			return err
		}
		_, err = q.db.Exec(ctx, `DELETE FROM messages WHERE id = ANY($1)`, ids)
		return err
	})
	return len(ids), keys, err
}

// DeleteOrphanIngestLog removes log lines no message owns that are older than the cutoff.
func (q *Queries) DeleteOrphanIngestLog(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := q.db.Exec(ctx, `DELETE FROM ingest_log WHERE message_id IS NULL AND at < $1`, cutoff)
	return tag.RowsAffected(), err
}

// ResetAfterPurge clears what is derived from stored mail: the log, address counters,
// last-message times and finished import records.
func (q *Queries) ResetAfterPurge(ctx context.Context) error {
	for _, sql := range []string{
		`DELETE FROM ingest_log`,
		`DELETE FROM business_addresses`,
		`UPDATE businesses SET last_message_at = NULL`,
		`DELETE FROM backfills WHERE status IN ('done','failed','cancelled')`,
	} {
		if _, err := q.db.Exec(ctx, sql); err != nil {
			return err
		}
	}
	return nil
}
