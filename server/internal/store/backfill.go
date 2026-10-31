package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Backfill is one business's import of older Gmail mail.
type Backfill struct {
	ID           uuid.UUID
	BusinessID   uuid.UUID
	BusinessName string
	Domain       string
	Since        time.Time
	Until        *time.Time
	Status       string // queued | running | paused | done | failed | cancelled
	PageToken    *string
	Estimated    *int
	Scanned      int
	Imported     int
	Sorted       int
	Unrouted     int
	Skipped      int
	PausedUntil  *time.Time
	LastError    *string
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
}

const backfillSelect = `SELECT bf.id, bf.business_id, b.name, b.domain, bf.since, bf.until, bf.status, bf.page_token, bf.estimated,
bf.scanned, bf.imported, bf.sorted, bf.unrouted, bf.skipped, bf.paused_until, bf.last_error, bf.created_at, bf.started_at, bf.finished_at
FROM backfills bf JOIN businesses b ON b.id = bf.business_id`

type scanner interface{ Scan(...any) error }

func scanBackfill(r scanner) (Backfill, error) {
	var b Backfill
	err := r.Scan(&b.ID, &b.BusinessID, &b.BusinessName, &b.Domain, &b.Since, &b.Until, &b.Status, &b.PageToken, &b.Estimated,
		&b.Scanned, &b.Imported, &b.Sorted, &b.Unrouted, &b.Skipped, &b.PausedUntil, &b.LastError, &b.CreatedAt, &b.StartedAt, &b.FinishedAt)
	return b, err
}

// CreateBackfill inserts a queued backfill.
func (q *Queries) CreateBackfill(ctx context.Context, id, business uuid.UUID, since time.Time, estimated int) error {
	_, err := q.db.Exec(ctx, `INSERT INTO backfills (id, business_id, since, status, estimated) VALUES ($1,$2,$3,'queued',$4)`,
		id, business, since, estimated)
	return err
}

func (q *Queries) GetBackfill(ctx context.Context, id uuid.UUID) (Backfill, error) {
	b, err := scanBackfill(q.db.QueryRow(ctx, backfillSelect+` WHERE bf.id=$1`, id))
	return b, notFound(err)
}

// ListBackfills returns all backfills, newest first.
func (q *Queries) ListBackfills(ctx context.Context) ([]Backfill, error) {
	rows, err := q.db.Query(ctx, backfillSelect+` ORDER BY bf.created_at DESC, bf.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Backfill{}
	for rows.Next() {
		b, err := scanBackfill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ActiveBackfillBusinesses returns the businesses that have a queued, running or paused backfill.
func (q *Queries) ActiveBackfillBusinesses(ctx context.Context) (map[uuid.UUID]bool, error) {
	rows, err := q.db.Query(ctx, `SELECT business_id FROM backfills WHERE status IN ('queued','running','paused')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// BackfillProgress adds to the counters and stores the page token; the status is only moved
// out of "running" by the caller, so a Pause or Cancel that landed meanwhile is kept.
type BackfillProgress struct {
	Scanned, Imported, Sorted, Unrouted, Skipped int
	PageToken                                    *string
}

func (q *Queries) AddBackfillProgress(ctx context.Context, id uuid.UUID, p BackfillProgress) error {
	_, err := q.db.Exec(ctx, `UPDATE backfills SET scanned=scanned+$2, imported=imported+$3, sorted=sorted+$4,
unrouted=unrouted+$5, skipped=skipped+$6, page_token=$7 WHERE id=$1`,
		id, p.Scanned, p.Imported, p.Sorted, p.Unrouted, p.Skipped, p.PageToken)
	return err
}

// MarkBackfillRunning moves queued/paused-by-timer rows to running; returns false if the row is
// not runnable (paused by the user, cancelled, done).
func (q *Queries) MarkBackfillRunning(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := q.db.Exec(ctx, `UPDATE backfills SET status='running', paused_until=NULL, last_error=NULL,
started_at=COALESCE(started_at, now()) WHERE id=$1 AND (status IN ('queued','running') OR (status='paused' AND paused_until IS NOT NULL AND paused_until <= now()))`, id)
	return tag.RowsAffected() == 1, err
}

// SetBackfillPausedByGmail pauses a running backfill until the rate limit window ends.
func (q *Queries) SetBackfillRateLimited(ctx context.Context, id uuid.UUID, until time.Time) error {
	_, err := q.db.Exec(ctx, `UPDATE backfills SET status='paused', paused_until=$2, last_error='rate_limited' WHERE id=$1 AND status='running'`, id, until)
	return err
}

// SetBackfillSignedOut pauses a running backfill until Gmail is connected again.
func (q *Queries) SetBackfillSignedOut(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, `UPDATE backfills SET status='paused', paused_until=NULL, last_error='gmail_signed_out' WHERE id=$1 AND status IN ('running','queued')`, id)
	return err
}

// FinishBackfill ends a running backfill as done or failed.
func (q *Queries) FinishBackfill(ctx context.Context, id uuid.UUID, status string, lastErr *string) error {
	_, err := q.db.Exec(ctx, `UPDATE backfills SET status=$2, last_error=$3, finished_at=now(), page_token=NULL
WHERE id=$1 AND status IN ('running','queued')`, id, status, lastErr)
	return err
}

// PauseBackfill is the user's pause: running or queued -> paused with no timer.
func (q *Queries) PauseBackfill(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := q.db.Exec(ctx, `UPDATE backfills SET status='paused', paused_until=NULL, last_error=NULL WHERE id=$1 AND status IN ('running','queued')`, id)
	return tag.RowsAffected() == 1, err
}

// ResumeBackfill puts a paused backfill back in the queue.
func (q *Queries) ResumeBackfill(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := q.db.Exec(ctx, `UPDATE backfills SET status='queued', paused_until=NULL, last_error=NULL WHERE id=$1 AND status='paused'`, id)
	return tag.RowsAffected() == 1, err
}

// CancelBackfill ends a backfill that is not finished yet.
func (q *Queries) CancelBackfill(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := q.db.Exec(ctx, `UPDATE backfills SET status='cancelled', finished_at=now(), paused_until=NULL, page_token=NULL
WHERE id=$1 AND status IN ('queued','running','paused')`, id)
	return tag.RowsAffected() == 1, err
}

// ResumeSignedOutBackfills requeues backfills paused because Gmail was signed out and returns their ids.
func (q *Queries) ResumeSignedOutBackfills(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := q.db.Query(ctx, `UPDATE backfills SET status='queued', last_error=NULL WHERE status='paused' AND last_error='gmail_signed_out' RETURNING id`)
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

// HasMessageIDHdr reports whether a message with this Message-ID is stored.
func (q *Queries) HasMessageIDHdr(ctx context.Context, hdr string) (bool, error) {
	var ok bool
	err := q.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM messages WHERE message_id_hdr=$1)`, hdr).Scan(&ok)
	return ok, err
}

// SetOrigMessageID remembers the email's own Message-ID on a failed row.
func (q *Queries) SetOrigMessageID(ctx context.Context, id uuid.UUID, hdr string) error {
	_, err := q.db.Exec(ctx, `UPDATE messages SET orig_message_id=$2 WHERE id=$1`, id, hdr)
	return err
}

func (q *Queries) GetOrigMessageID(ctx context.Context, id uuid.UUID) (string, error) {
	var h *string
	err := q.db.QueryRow(ctx, `SELECT orig_message_id FROM messages WHERE id=$1`, id).Scan(&h)
	if h == nil {
		return "", notFound(err)
	}
	return *h, notFound(err)
}

// MessageRoute returns a message's route (pending | routed | unrouted).
func (q *Queries) MessageRoute(ctx context.Context, id uuid.UUID) (string, error) {
	var r string
	err := q.db.QueryRow(ctx, `SELECT route::text FROM messages WHERE id=$1`, id).Scan(&r)
	return r, notFound(err)
}

// SetGmailIDByHdr records the Gmail ids on an already stored message that lacks them.
func (q *Queries) SetGmailIDByHdr(ctx context.Context, hdr, gmailID, threadID string) error {
	_, err := q.db.Exec(ctx, `UPDATE messages SET gmail_id=$2, gmail_thread_id=$3 WHERE message_id_hdr=$1 AND gmail_id IS NULL`, hdr, gmailID, threadID)
	return err
}
