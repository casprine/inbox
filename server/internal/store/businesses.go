package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Business struct {
	ID               uuid.UUID
	Name             string
	Domain           string
	CFZoneID         string
	SortOrder        int
	Hidden           bool
	Enabled          bool
	Routing          string // ok | to_email | none | unknown
	RoutingCheckedAt *time.Time
	LastMessageAt    *time.Time
	CreatedAt        time.Time
}

type BusinessWithUnread struct {
	Business
	Unread int
}

const businessCols = `id, name, domain, cf_zone_id, sort_order, hidden, enabled, routing::text, routing_checked_at, last_message_at, created_at`

func scanBusiness(row pgx.Row, extra ...any) (Business, error) {
	var b Business
	dest := append([]any{&b.ID, &b.Name, &b.Domain, &b.CFZoneID, &b.SortOrder, &b.Hidden, &b.Enabled,
		&b.Routing, &b.RoutingCheckedAt, &b.LastMessageAt, &b.CreatedAt}, extra...)
	return b, row.Scan(dest...)
}

func (q *Queries) CreateBusiness(ctx context.Context, b Business) (Business, error) {
	if b.Routing == "" {
		b.Routing = "unknown"
	}
	return scanBusiness(q.db.QueryRow(ctx, `
INSERT INTO businesses (id, name, domain, cf_zone_id, sort_order, hidden, enabled, routing)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8::routing_state) RETURNING `+businessCols,
		b.ID, b.Name, b.Domain, b.CFZoneID, b.SortOrder, b.Hidden, b.Enabled, b.Routing))
}

func (q *Queries) GetBusiness(ctx context.Context, id uuid.UUID) (Business, error) {
	b, err := scanBusiness(q.db.QueryRow(ctx, `SELECT `+businessCols+` FROM businesses WHERE id=$1`, id))
	return b, notFound(err)
}

func (q *Queries) GetBusinessByDomain(ctx context.Context, domain string) (Business, error) {
	b, err := scanBusiness(q.db.QueryRow(ctx, `SELECT `+businessCols+` FROM businesses WHERE domain=$1`, domain))
	return b, notFound(err)
}

// ListBusinessesWithUnread returns all businesses ordered for display, with
// the unread count (unread, not archived, stored) computed from the partial index.
func (q *Queries) ListBusinessesWithUnread(ctx context.Context) ([]BusinessWithUnread, error) {
	rows, err := q.db.Query(ctx, `
SELECT b.id, b.name, b.domain, b.cf_zone_id, b.sort_order, b.hidden, b.enabled, b.routing::text,
       b.routing_checked_at, b.last_message_at, b.created_at,
       (SELECT count(*) FROM messages m
         WHERE m.business_id = b.id AND NOT m.is_read AND m.archived_at IS NULL AND m.state = 'stored')
FROM businesses b ORDER BY b.sort_order, b.created_at, b.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BusinessWithUnread
	for rows.Next() {
		var b BusinessWithUnread
		if err := rows.Scan(&b.ID, &b.Name, &b.Domain, &b.CFZoneID, &b.SortOrder, &b.Hidden, &b.Enabled,
			&b.Routing, &b.RoutingCheckedAt, &b.LastMessageAt, &b.CreatedAt, &b.Unread); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BusinessPatch holds optional fields; nil means unchanged.
type BusinessPatch struct {
	Name    *string
	Hidden  *bool
	Enabled *bool
	Routing *string
}

func (q *Queries) UpdateBusiness(ctx context.Context, id uuid.UUID, p BusinessPatch) (Business, error) {
	b, err := scanBusiness(q.db.QueryRow(ctx, `
UPDATE businesses SET
  name    = COALESCE($2, name),
  hidden  = COALESCE($3, hidden),
  enabled = COALESCE($4, enabled),
  routing = COALESCE($5::routing_state, routing),
  routing_checked_at = CASE WHEN $5::text IS NULL THEN routing_checked_at ELSE now() END
WHERE id=$1 RETURNING `+businessCols, id, p.Name, p.Hidden, p.Enabled, p.Routing))
	return b, notFound(err)
}

// SetBusinessOrder assigns sort_order by position in ids.
func (q *Queries) SetBusinessOrder(ctx context.Context, ids []uuid.UUID) error {
	_, err := q.db.Exec(ctx, `
UPDATE businesses b SET sort_order = o.ord - 1
FROM unnest($1::uuid[]) WITH ORDINALITY AS o(id, ord) WHERE b.id = o.id`, ids)
	return err
}

func (q *Queries) DeleteBusiness(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, `DELETE FROM businesses WHERE id=$1`, id)
	return err
}

type BusinessAddress struct {
	BusinessID uuid.UUID
	LocalPart  string
	MsgCount   int
	LastAt     *time.Time
}

// UpsertBusinessAddress counts one more message for local_part, keeping the latest last_at.
func (q *Queries) UpsertBusinessAddress(ctx context.Context, businessID uuid.UUID, localPart string, at time.Time) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO business_addresses (business_id, local_part, msg_count, last_at) VALUES ($1,$2,1,$3)
ON CONFLICT (business_id, local_part) DO UPDATE
SET msg_count = business_addresses.msg_count + 1,
    last_at = GREATEST(business_addresses.last_at, EXCLUDED.last_at)`, businessID, localPart, at)
	return err
}

func (q *Queries) ListBusinessAddresses(ctx context.Context, businessID uuid.UUID) ([]BusinessAddress, error) {
	rows, err := q.db.Query(ctx, `
SELECT business_id, local_part, msg_count, last_at FROM business_addresses
WHERE business_id=$1 ORDER BY msg_count DESC, local_part`, businessID)
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

// TouchBusinessLastMessage bumps last_message_at monotonically.
func (q *Queries) TouchBusinessLastMessage(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := q.db.Exec(ctx, `UPDATE businesses SET last_message_at = GREATEST(last_message_at, $2) WHERE id=$1`, id, at)
	return err
}

// ListBusinesses returns every business in display order (no counts).
func (q *Queries) ListBusinesses(ctx context.Context) ([]Business, error) {
	rows, err := q.db.Query(ctx, `SELECT `+businessCols+` FROM businesses ORDER BY sort_order, created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Business
	for rows.Next() {
		b, err := scanBusiness(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// UpdateBusinessRouting records a routing check result. Returns the previous state.
func (q *Queries) UpdateBusinessRouting(ctx context.Context, id uuid.UUID, routing string) (prev string, err error) {
	err = q.db.QueryRow(ctx, `
UPDATE businesses b SET routing=$2::routing_state, routing_checked_at=now()
FROM (SELECT id, routing::text AS prev FROM businesses WHERE id=$1 FOR UPDATE) o
WHERE b.id=o.id RETURNING o.prev`, id, routing).Scan(&prev)
	return prev, notFound(err)
}

// SetBusinessZone stores the Cloudflare zone id found for a business's domain.
func (q *Queries) SetBusinessZone(ctx context.Context, id uuid.UUID, zoneID string) error {
	_, err := q.db.Exec(ctx, `UPDATE businesses SET cf_zone_id=$2 WHERE id=$1`, id, zoneID)
	return err
}
