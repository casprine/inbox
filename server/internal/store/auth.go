package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Device is a paired client.
type Device struct {
	ID         uuid.UUID
	Name       string
	CreatedAt  time.Time
	LastSeenAt *time.Time
	RevokedAt  *time.Time
}

// CreateDevice inserts a device; tokenHash is the raw sha256 bytes of the token.
func (q *Queries) CreateDevice(ctx context.Context, id uuid.UUID, name string, tokenHash []byte) error {
	_, err := q.db.Exec(ctx, `INSERT INTO devices (id, name, token_hash) VALUES ($1,$2,$3)`,
		id, name, tokenHash)
	return err
}

// DeviceByTokenHash returns the non-revoked device owning the token hash, or ErrNotFound.
func (q *Queries) DeviceByTokenHash(ctx context.Context, tokenHash []byte) (Device, error) {
	var d Device
	var name *string
	err := q.db.QueryRow(ctx, `SELECT id, name, created_at, last_seen_at, revoked_at
		FROM devices WHERE token_hash=$1 AND revoked_at IS NULL`, tokenHash).
		Scan(&d.ID, &name, &d.CreatedAt, &d.LastSeenAt, &d.RevokedAt)
	if name != nil {
		d.Name = *name
	}
	return d, notFound(err)
}

// TouchDevice sets last_seen_at to now.
func (q *Queries) TouchDevice(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, `UPDATE devices SET last_seen_at=now() WHERE id=$1`, id)
	return err
}

// RevokeDevice marks a device revoked; returns false when it does not exist or was already revoked.
func (q *Queries) RevokeDevice(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := q.db.Exec(ctx, `UPDATE devices SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, id)
	return tag.RowsAffected() == 1, err
}

// GetIdempotentResponse returns the stored response for (device,key) if younger than maxAge.
func (q *Queries) GetIdempotentResponse(ctx context.Context, deviceID uuid.UUID, key string, maxAge time.Duration) (json.RawMessage, error) {
	var v []byte
	err := q.db.QueryRow(ctx, `SELECT response FROM idempotency_keys
		WHERE device_id=$1 AND key=$2 AND at > now() - make_interval(secs => $3)`,
		deviceID, key, maxAge.Seconds()).Scan(&v)
	return v, notFound(err)
}

// Outcomes of ClaimIdempotency.
const (
	IdemClaimed    = "claimed"     // the caller owns the key and must run the handler
	IdemReplay     = "replay"      // a finished response is stored
	IdemInProgress = "in_progress" // another request holds the claim right now
)

// ClaimIdempotency atomically claims (device, key) before the handler runs, so two simultaneous requests
// with the same key cannot both execute. An expired stored response (older than ttl) and an abandoned
// claim (older than inflight, e.g. after a crash) are taken over. For IdemReplay the stored response is returned.
func (q *Queries) ClaimIdempotency(ctx context.Context, deviceID uuid.UUID, key string, ttl, inflight time.Duration) (string, json.RawMessage, error) {
	var claimed bool
	err := q.db.QueryRow(ctx, `
INSERT INTO idempotency_keys (key, device_id, response, at) VALUES ($1,$2,NULL,now())
ON CONFLICT (device_id, key) DO UPDATE SET response = NULL, at = now()
WHERE (idempotency_keys.response IS NOT NULL AND idempotency_keys.at < now() - make_interval(secs => $3))
   OR (idempotency_keys.response IS NULL     AND idempotency_keys.at < now() - make_interval(secs => $4))
RETURNING true`, key, deviceID, ttl.Seconds(), inflight.Seconds()).Scan(&claimed)
	if err == nil {
		return IdemClaimed, nil, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", nil, err
	}
	var resp []byte
	err = q.db.QueryRow(ctx, `SELECT response FROM idempotency_keys WHERE device_id=$1 AND key=$2`, deviceID, key).Scan(&resp)
	if errors.Is(err, pgx.ErrNoRows) { // released between our two statements: the caller can simply retry
		return IdemInProgress, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if resp == nil {
		return IdemInProgress, nil, nil
	}
	return IdemReplay, resp, nil
}

// PutIdempotentResponse completes a claim by storing the response.
func (q *Queries) PutIdempotentResponse(ctx context.Context, deviceID uuid.UUID, key string, response any) error {
	b, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = q.db.Exec(ctx, `UPDATE idempotency_keys SET response=$3, at=now() WHERE device_id=$1 AND key=$2`, deviceID, key, b)
	return err
}

// ReleaseIdempotency drops an unfinished claim (the handler failed) so the client can retry with the same key.
func (q *Queries) ReleaseIdempotency(ctx context.Context, deviceID uuid.UUID, key string) error {
	_, err := q.db.Exec(ctx, `DELETE FROM idempotency_keys WHERE device_id=$1 AND key=$2 AND response IS NULL`, deviceID, key)
	return err
}
