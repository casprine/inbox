package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GmailAccount is the single connected Gmail account.
type GmailAccount struct {
	ID              uuid.UUID
	Email           string
	RefreshTokenEnc []byte
	KeyID           string
	Status          string // connected | needs_reauth | revoked
	ConnectedAt     time.Time
	LastWriteAt     *time.Time
}

const gmailAccountCols = `id, email, refresh_token_enc, key_id, status, connected_at, last_write_at`

func scanGmailAccount(row interface{ Scan(...any) error }) (GmailAccount, error) {
	var a GmailAccount
	err := row.Scan(&a.ID, &a.Email, &a.RefreshTokenEnc, &a.KeyID, &a.Status, &a.ConnectedAt, &a.LastWriteAt)
	return a, notFound(err)
}

// GetGmailAccount returns the connected account or ErrNotFound.
func (q *Queries) GetGmailAccount(ctx context.Context) (GmailAccount, error) {
	return scanGmailAccount(q.db.QueryRow(ctx, `SELECT `+gmailAccountCols+` FROM gmail_accounts LIMIT 1`))
}

// UpsertGmailAccount stores the account. The same email keeps its row (and label cache);
// a different email replaces the old account.
func (q *Queries) UpsertGmailAccount(ctx context.Context, id uuid.UUID, email string, enc []byte, keyID string) (GmailAccount, error) {
	if _, err := q.db.Exec(ctx, `DELETE FROM gmail_accounts WHERE email <> $1`, email); err != nil {
		return GmailAccount{}, err
	}
	return scanGmailAccount(q.db.QueryRow(ctx, `
INSERT INTO gmail_accounts (id, email, refresh_token_enc, key_id, status, connected_at)
VALUES ($1,$2,$3,$4,'connected', now())
ON CONFLICT ((true)) DO UPDATE
SET refresh_token_enc=EXCLUDED.refresh_token_enc, key_id=EXCLUDED.key_id, status='connected', connected_at=now()
RETURNING `+gmailAccountCols, id, email, enc, keyID))
}

// SetGmailAccountStatus updates the status; false when no account exists.
func (q *Queries) SetGmailAccountStatus(ctx context.Context, id uuid.UUID, status string) (bool, error) {
	tag, err := q.db.Exec(ctx, `UPDATE gmail_accounts SET status=$2 WHERE id=$1`, id, status)
	return tag.RowsAffected() == 1, err
}

// TouchGmailWrite records a successful Gmail write.
func (q *Queries) TouchGmailWrite(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, `UPDATE gmail_accounts SET last_write_at=now() WHERE id=$1`, id)
	return err
}

// DeleteGmailAccount removes the account (labels cascade) and returns it.
func (q *Queries) DeleteGmailAccount(ctx context.Context) (GmailAccount, error) {
	return scanGmailAccount(q.db.QueryRow(ctx, `DELETE FROM gmail_accounts RETURNING `+gmailAccountCols))
}

// ResumePausedGmailSync flips every paused row to pending, due now. Returns the count.
func (q *Queries) ResumePausedGmailSync(ctx context.Context) (int64, error) {
	tag, err := q.db.Exec(ctx, `UPDATE gmail_sync SET state='pending', attempts=0, next_attempt_at=now(), updated_at=now()
WHERE state='paused'`)
	return tag.RowsAffected(), err
}

// PauseGmailSync parks all pending rows until Gmail is connected again.
func (q *Queries) PauseGmailSync(ctx context.Context) (int64, error) {
	tag, err := q.db.Exec(ctx, `UPDATE gmail_sync SET state='paused', updated_at=now() WHERE state='pending'`)
	return tag.RowsAffected(), err
}

// CountGmailWaiting is the number of actions not yet applied in Gmail (pending or paused).
func (q *Queries) CountGmailWaiting(ctx context.Context) (int, error) {
	var n int
	err := q.db.QueryRow(ctx, `SELECT count(*) FROM gmail_sync WHERE state IN ('pending','paused')`).Scan(&n)
	return n, err
}

// CreateGmailOAuthState stores a pending OAuth handshake.
func (q *Queries) CreateGmailOAuthState(ctx context.Context, state string, device *uuid.UUID, verifier string, ttl time.Duration) error {
	_, err := q.db.Exec(ctx, `INSERT INTO gmail_oauth_states (state, device_id, verifier, expires_at)
VALUES ($1,$2,$3, now() + make_interval(secs => $4))`, state, device, verifier, ttl.Seconds())
	return err
}

// ConsumeGmailOAuthState deletes and returns the verifier for an unexpired state (single use).
func (q *Queries) ConsumeGmailOAuthState(ctx context.Context, state string) (verifier string, err error) {
	err = q.db.QueryRow(ctx, `DELETE FROM gmail_oauth_states WHERE state=$1 AND expires_at > now() RETURNING verifier`, state).Scan(&verifier)
	return verifier, notFound(err)
}

// DeleteExpiredGmailOAuthStates supports the prune job.
func (q *Queries) DeleteExpiredGmailOAuthStates(ctx context.Context) (int64, error) {
	tag, err := q.db.Exec(ctx, `DELETE FROM gmail_oauth_states WHERE expires_at < now()`)
	return tag.RowsAffected(), err
}

// SyncRow is a gmail_sync row joined with what the engine needs from its message.
type SyncRow struct {
	MessageID     uuid.UUID
	MessageIDHdr  string
	GmailID       *string
	GmailThreadID *string
	WantLabel     *string
	WantArchived  bool
	WantRead      bool
	Applied       json.RawMessage
	State         string
	Attempts      int
	LabelsOnly    bool
	LastError     *string
}

// ClaimDueGmailSync leases up to limit pending rows that are due: their next_attempt_at moves
// `lease` into the future so a crashed worker's rows come back, and concurrent engines never
// take the same row. The result reflects the want_* values at claim time.
func (q *Queries) ClaimDueGmailSync(ctx context.Context, limit int, lease time.Duration) ([]SyncRow, error) {
	rows, err := q.db.Query(ctx, `
WITH due AS (
  SELECT message_id FROM gmail_sync
  WHERE state='pending' AND next_attempt_at <= now()
  ORDER BY next_attempt_at LIMIT $1 FOR UPDATE SKIP LOCKED
), claimed AS (
  UPDATE gmail_sync g SET next_attempt_at = now() + make_interval(secs => $2), last_attempt_at = now()
  FROM due WHERE g.message_id = due.message_id
  RETURNING g.*
)
SELECT c.message_id, m.message_id_hdr, m.gmail_id, m.gmail_thread_id, c.want_label, c.want_archived, c.want_read,
       c.applied, c.state::text, c.attempts, c.labels_only, c.last_error
FROM claimed c JOIN messages m ON m.id = c.message_id`, limit, lease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncRow
	for rows.Next() {
		var r SyncRow
		if err := rows.Scan(&r.MessageID, &r.MessageIDHdr, &r.GmailID, &r.GmailThreadID, &r.WantLabel, &r.WantArchived,
			&r.WantRead, &r.Applied, &r.State, &r.Attempts, &r.LabelsOnly, &r.LastError); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SyncOutcome is what the engine decided for a claimed row.
type SyncOutcome struct {
	State     string // pending | synced | not_found | failed | paused
	Attempts  int
	NextAfter time.Duration // for pending: delay until the next try
	LastError *string
	Applied   *json.RawMessage // set when Gmail was brought to the wants in `Seen`
}

// FinishGmailSync records the outcome. When Applied is set (a successful sync), the row only
// becomes synced if the wants did not change while the engine worked; otherwise it stays
// pending with whatever schedule the newer want set.
func (q *Queries) FinishGmailSync(ctx context.Context, r SyncRow, o SyncOutcome) error {
	if o.Applied != nil {
		_, err := q.db.Exec(ctx, `
UPDATE gmail_sync SET applied=$2, attempts=0, last_error=NULL, last_synced_at=now(), updated_at=now(),
  state = CASE WHEN want_label IS NOT DISTINCT FROM $3 AND want_archived=$4 AND want_read=$5 THEN 'synced'::gmail_state ELSE state END
WHERE message_id=$1`, r.MessageID, []byte(*o.Applied), r.WantLabel, r.WantArchived, r.WantRead)
		return err
	}
	_, err := q.db.Exec(ctx, `
UPDATE gmail_sync SET state=$2::gmail_state, attempts=$3, last_error=$4, updated_at=now(),
  next_attempt_at = CASE WHEN $2 = 'pending' THEN now() + make_interval(secs => $5) ELSE next_attempt_at END
WHERE message_id=$1 AND (want_label IS NOT DISTINCT FROM $6 AND want_archived=$7 AND want_read=$8 OR $2 <> 'failed' AND $2 <> 'not_found')`,
		r.MessageID, o.State, o.Attempts, o.LastError, o.NextAfter.Seconds(), r.WantLabel, r.WantArchived, r.WantRead)
	return err
}

// SetMessageGmailIDs caches the Gmail ids found by lookup.
func (q *Queries) SetMessageGmailIDs(ctx context.Context, id uuid.UUID, gmailID, threadID string) error {
	_, err := q.db.Exec(ctx, `UPDATE messages SET gmail_id=$2, gmail_thread_id=$3 WHERE id=$1`, id, gmailID, threadID)
	return err
}

// ClearMessageGmailIDs forgets cached ids (the message vanished from Gmail).
func (q *Queries) ClearMessageGmailIDs(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, `UPDATE messages SET gmail_id=NULL, gmail_thread_id=NULL WHERE id=$1`, id)
	return err
}

// RetryGmailSync puts a failed or not_found row back to pending, due now.
// Returns (found, retried): found=false means no row; retried=false means the row was not retryable.
func (q *Queries) RetryGmailSync(ctx context.Context, id uuid.UUID) (found, retried bool, err error) {
	tag, err := q.db.Exec(ctx, `UPDATE gmail_sync SET state='pending', attempts=0, last_error=NULL, next_attempt_at=now(), updated_at=now()
WHERE message_id=$1 AND state IN ('failed','not_found')`, id)
	if err != nil {
		return false, false, err
	}
	if tag.RowsAffected() == 1 {
		return true, true, nil
	}
	err = q.db.QueryRow(ctx, `SELECT true FROM gmail_sync WHERE message_id=$1`, id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	return found, false, err
}

// GmailLabels returns the cached name -> Gmail label id map for the account.
func (q *Queries) GmailLabels(ctx context.Context, account uuid.UUID) (map[string]string, error) {
	rows, err := q.db.Query(ctx, `SELECT name, gmail_label_id FROM gmail_labels WHERE account_id=$1`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var n, id string
		if err := rows.Scan(&n, &id); err != nil {
			return nil, err
		}
		out[n] = id
	}
	return out, rows.Err()
}

// ReplaceGmailLabels swaps the cache for the given name -> id map.
func (q *Queries) ReplaceGmailLabels(ctx context.Context, account uuid.UUID, labels map[string]string) error {
	if _, err := q.db.Exec(ctx, `DELETE FROM gmail_labels WHERE account_id=$1`, account); err != nil {
		return err
	}
	for n, id := range labels {
		if err := q.UpsertGmailLabel(ctx, account, n, id); err != nil {
			return err
		}
	}
	return nil
}

// UpsertGmailLabel caches one label.
func (q *Queries) UpsertGmailLabel(ctx context.Context, account uuid.UUID, name, labelID string) error {
	_, err := q.db.Exec(ctx, `INSERT INTO gmail_labels (account_id, name, gmail_label_id) VALUES ($1,$2,$3)
ON CONFLICT (account_id, name) DO UPDATE SET gmail_label_id=EXCLUDED.gmail_label_id`, account, name, labelID)
	return err
}

// MarkGmailSyncLabelsOnly makes a message's pending write-back apply labels only.
func (q *Queries) MarkGmailSyncLabelsOnly(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, `UPDATE gmail_sync SET labels_only=true WHERE message_id=$1`, id)
	return err
}

// ThreadBusiness returns the business of the earliest message in a thread that has one.
func (q *Queries) ThreadBusiness(ctx context.Context, threadKey string) (*uuid.UUID, error) {
	var id *uuid.UUID
	err := q.db.QueryRow(ctx, `SELECT business_id FROM messages WHERE thread_key=$1 AND business_id IS NOT NULL
ORDER BY received_at LIMIT 1`, threadKey).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return id, err
}

// MessageHasGmailID reports whether a stored message already carries this Gmail id.
func (q *Queries) MessageHasGmailID(ctx context.Context, gmailID string) (bool, error) {
	var ok bool
	err := q.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM messages WHERE gmail_id=$1)`, gmailID).Scan(&ok)
	return ok, err
}

// UpdateGmailAccountSeal replaces the sealed refresh token (key rotation).
func (q *Queries) UpdateGmailAccountSeal(ctx context.Context, id uuid.UUID, enc []byte, keyID string) error {
	_, err := q.db.Exec(ctx, `UPDATE gmail_accounts SET refresh_token_enc=$2, key_id=$3 WHERE id=$1`, id, enc, keyID)
	return err
}
