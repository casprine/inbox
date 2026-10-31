package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/casprine/inbox/server/internal/rules"
)

const ruleCols = `id, name, position, enabled, business_id, match_to_local, match_from, match_subject, gmail_label,
gmail_archive, gmail_mark_read, label_only, imported_from_filter, created_at, updated_at`

func scanRule(row pgx.Row) (rules.Rule, error) {
	var r rules.Rule
	err := row.Scan(&r.ID, &r.Name, &r.Position, &r.Enabled, &r.BusinessID, &r.MatchToLocal, &r.MatchFrom, &r.MatchSubject,
		&r.GmailLabel, &r.GmailArchive, &r.GmailMarkRead, &r.LabelOnly, &r.ImportedFromFilter, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// ListRules returns all rules (enabled or not) in priority order.
func (q *Queries) ListRules(ctx context.Context) ([]rules.Rule, error) {
	rows, err := q.db.Query(ctx, `SELECT `+ruleCols+` FROM rules ORDER BY position, created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []rules.Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RuleMessageCounts maps rule id to the number of messages it currently routes.
func (q *Queries) RuleMessageCounts(ctx context.Context) (map[uuid.UUID]int, error) {
	rows, err := q.db.Query(ctx, `SELECT rule_id, count(*) FROM messages WHERE rule_id IS NOT NULL GROUP BY rule_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func (q *Queries) GetRule(ctx context.Context, id uuid.UUID) (rules.Rule, error) {
	r, err := scanRule(q.db.QueryRow(ctx, `SELECT `+ruleCols+` FROM rules WHERE id=$1`, id))
	return r, notFound(err)
}

// CreateRuleTop inserts r above every existing rule (position 0, the rest shift down).
func (s *Store) CreateRuleTop(ctx context.Context, r rules.Rule) (rules.Rule, error) {
	var out rules.Rule
	err := s.WithTx(ctx, func(q *Queries) error {
		if _, err := q.db.Exec(ctx, `UPDATE rules SET position = position + 1`); err != nil {
			return err
		}
		var err error
		out, err = scanRule(q.db.QueryRow(ctx, `
INSERT INTO rules (id, name, position, enabled, business_id, match_to_local, match_from, match_subject, gmail_label,
  gmail_archive, gmail_mark_read, label_only, imported_from_filter)
VALUES ($1,$2,0,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING `+ruleCols,
			r.ID, r.Name, r.Enabled, r.BusinessID, r.MatchToLocal, r.MatchFrom, r.MatchSubject, r.GmailLabel,
			r.GmailArchive, r.GmailMarkRead, r.LabelOnly, r.ImportedFromFilter))
		return err
	})
	return out, err
}

// UpdateRule writes every editable field of r (position is left alone).
func (q *Queries) UpdateRule(ctx context.Context, r rules.Rule) (rules.Rule, error) {
	out, err := scanRule(q.db.QueryRow(ctx, `
UPDATE rules SET name=$2, enabled=$3, business_id=$4, match_to_local=$5, match_from=$6, match_subject=$7, gmail_label=$8,
  gmail_archive=$9, gmail_mark_read=$10, label_only=$11, updated_at=now()
WHERE id=$1 RETURNING `+ruleCols,
		r.ID, r.Name, r.Enabled, r.BusinessID, r.MatchToLocal, r.MatchFrom, r.MatchSubject, r.GmailLabel,
		r.GmailArchive, r.GmailMarkRead, r.LabelOnly))
	return out, notFound(err)
}

// DeleteRule removes a rule; messages it routed go back to unrouted so they can be re-evaluated.
func (s *Store) DeleteRule(ctx context.Context, id uuid.UUID) error {
	return s.WithTx(ctx, func(q *Queries) error {
		if _, err := q.GetRule(ctx, id); err != nil {
			return err
		}
		if _, err := q.db.Exec(ctx, `UPDATE messages SET route='unrouted' WHERE rule_id=$1`, id); err != nil {
			return err
		}
		if _, err := q.db.Exec(ctx, `DELETE FROM rules WHERE id=$1`, id); err != nil {
			return err
		}
		_, err := q.db.Exec(ctx, `
UPDATE rules r SET position = n.pos FROM (SELECT id, row_number() OVER (ORDER BY position, created_at, id) - 1 AS pos FROM rules) n
WHERE r.id = n.id AND r.position <> n.pos`)
		return err
	})
}

// SetRuleOrder puts the listed rules first, in the given order; unlisted rules follow in their
// current relative order. It returns ErrNotFound if an id is unknown.
func (s *Store) SetRuleOrder(ctx context.Context, ids []uuid.UUID) error {
	return s.WithTx(ctx, func(q *Queries) error {
		cur, err := q.ListRules(ctx)
		if err != nil {
			return err
		}
		known := map[uuid.UUID]bool{}
		for _, r := range cur {
			known[r.ID] = true
		}
		order := make([]uuid.UUID, 0, len(cur))
		seen := map[uuid.UUID]bool{}
		for _, id := range ids {
			if !known[id] || seen[id] {
				return ErrNotFound
			}
			seen[id] = true
			order = append(order, id)
		}
		for _, r := range cur {
			if !seen[r.ID] {
				order = append(order, r.ID)
			}
		}
		_, err = q.db.Exec(ctx, `
UPDATE rules r SET position = o.ord - 1 FROM unnest($1::uuid[]) WITH ORDINALITY AS o(id, ord) WHERE r.id = o.id`, order)
		return err
	})
}

// RuleMessages returns inbound stored messages for re-evaluation, ordered by id, those with
// id > after. unroutedOnly restricts to route='unrouted'.
func (q *Queries) RuleMessages(ctx context.Context, unroutedOnly bool, after uuid.UUID, limit int) ([]Message, error) {
	rows, err := q.db.Query(ctx, `SELECT `+messageCols+` FROM messages
WHERE state='stored' AND direction='in' AND id > $1 AND (NOT $2 OR route='unrouted') ORDER BY id LIMIT $3`, after, unroutedOnly, limit)
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

// RecentInbound returns the newest inbound stored messages for the rule tester.
func (q *Queries) RecentInbound(ctx context.Context, unroutedOnly bool, limit int) ([]Message, error) {
	rows, err := q.db.Query(ctx, `SELECT `+messageCols+` FROM messages
WHERE state='stored' AND direction='in' AND (NOT $1 OR route='unrouted') ORDER BY received_at DESC, id DESC LIMIT $2`, unroutedOnly, limit)
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

// WantGmail upserts the desired Gmail state for a message and schedules write-back after delay.
// The label is replaced; archive and read are only ever raised here so a user's own action is kept.
func (q *Queries) WantGmail(ctx context.Context, id uuid.UUID, label *string, archived, read bool, delay time.Duration) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO gmail_sync (message_id, want_label, want_archived, want_read, state, attempts, next_attempt_at)
VALUES ($1,$2,$3,$4,'pending',0, now() + make_interval(secs => $5))
ON CONFLICT (message_id) DO UPDATE
SET want_label=EXCLUDED.want_label, want_archived=gmail_sync.want_archived OR EXCLUDED.want_archived,
    want_read=gmail_sync.want_read OR EXCLUDED.want_read,
    state='pending', attempts=0, next_attempt_at=EXCLUDED.next_attempt_at, updated_at=now()`,
		id, label, archived, read, delay.Seconds())
	return err
}

// ImportedFilterIDs is the set of Gmail filter ids already turned into rules.
func (q *Queries) ImportedFilterIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := q.db.Query(ctx, `SELECT imported_from_filter FROM rules WHERE imported_from_filter IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
