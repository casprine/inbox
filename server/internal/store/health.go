package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// BusinessActivity is what the health checks need per enabled business.
type BusinessActivity struct {
	Business
	// MedianGapSecs is the median seconds between messages over the last 30 days (nil: fewer than two).
	MedianGapSecs *float64
}

// BusinessActivities returns enabled businesses with their 30-day median message gap.
func (q *Queries) BusinessActivities(ctx context.Context, now time.Time) ([]BusinessActivity, error) {
	rows, err := q.db.Query(ctx, `
SELECT `+prefixed("b", businessCols)+`,
  (SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY gap) FROM (
     SELECT extract(epoch FROM received_at - lag(received_at) OVER (ORDER BY received_at)) AS gap
     FROM messages WHERE business_id=b.id AND direction='in' AND state='stored' AND received_at > $1::timestamptz - interval '30 days' AND received_at <= $1) g
   WHERE gap IS NOT NULL)
FROM businesses b WHERE b.enabled ORDER BY b.sort_order, b.created_at, b.id`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BusinessActivity
	for rows.Next() {
		var a BusinessActivity
		b, err := scanBusiness(rows, &a.MedianGapSecs)
		if err != nil {
			return nil, err
		}
		a.Business = b
		out = append(out, a)
	}
	return out, rows.Err()
}

func prefixed(alias, cols string) string {
	out := ""
	start := 0
	for i := 0; i <= len(cols); i++ {
		if i == len(cols) || cols[i] == ',' {
			col := cols[start:i]
			for len(col) > 0 && (col[0] == ' ' || col[0] == '\n') {
				col = col[1:]
			}
			if out != "" {
				out += ", "
			}
			out += alias + "." + col
			start = i + 1
		}
	}
	return out
}

// IngestStats summarises the Worker's recent activity.
type IngestStats struct {
	LastReceivedAt *time.Time
	MedianGapSecs  *float64 // all businesses, last 30 days
	ErrorsLast24h  int      // ingest_log error lines
	Received24h    int
}

func (q *Queries) IngestStats(ctx context.Context, now time.Time) (IngestStats, error) {
	var s IngestStats
	err := q.db.QueryRow(ctx, `
SELECT (SELECT max(received_at) FROM messages WHERE direction='in' AND source='edge' AND received_at <= $1),
       (SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY gap) FROM (
          SELECT extract(epoch FROM received_at - lag(received_at) OVER (ORDER BY received_at)) AS gap
          FROM messages WHERE direction='in' AND source='edge' AND received_at > $1::timestamptz - interval '30 days' AND received_at <= $1) g WHERE gap IS NOT NULL),
       (SELECT count(*) FROM ingest_log WHERE level='error' AND at > $1::timestamptz - interval '24 hours' AND at <= $1),
       (SELECT count(*) FROM messages WHERE direction='in' AND source='edge' AND received_at > $1::timestamptz - interval '24 hours' AND received_at <= $1)`, now).
		Scan(&s.LastReceivedAt, &s.MedianGapSecs, &s.ErrorsLast24h, &s.Received24h)
	return s, err
}

// QueueStats reads River's job table.
type QueueStats struct {
	Available       int
	Retryable       int
	DiscardedLast24 int
	OldestAvailable *time.Time
}

func (q *Queries) QueueStats(ctx context.Context, now time.Time) (QueueStats, error) {
	var s QueueStats
	err := q.db.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE state='available'),
       count(*) FILTER (WHERE state='retryable'),
       count(*) FILTER (WHERE state='discarded' AND finalized_at > $1::timestamptz - interval '24 hours'),
       min(scheduled_at) FILTER (WHERE state='available' AND scheduled_at <= $1)
FROM river_job`, now).Scan(&s.Available, &s.Retryable, &s.DiscardedLast24, &s.OldestAvailable)
	return s, err
}

// AlfatherStatus is the paired Alfather consumer, if any.
type AlfatherStatus struct {
	ID         uuid.UUID
	CreatedAt  time.Time
	LastSeenAt *time.Time
	CursorSeq  int64
	Waiting    int
}

// GetAlfather returns the alfather consumer or ErrNotFound.
func (q *Queries) GetAlfather(ctx context.Context) (AlfatherStatus, error) {
	var a AlfatherStatus
	err := q.db.QueryRow(ctx, `
SELECT c.id, c.created_at, c.last_seen_at, c.cursor_seq,
  (SELECT count(*) FROM messages m WHERE m.seq > c.cursor_seq AND `+alfatherFeed+`)
FROM consumers c WHERE c.kind='alfather' ORDER BY c.created_at DESC LIMIT 1`).Scan(&a.ID, &a.CreatedAt, &a.LastSeenAt, &a.CursorSeq, &a.Waiting)
	return a, notFound(err)
}
