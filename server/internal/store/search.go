package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// SearchParams is the SQL-facing form of a parsed search query. Every string is bound as a
// parameter; nothing here is interpolated into SQL.
type SearchParams struct {
	// WebTerms are AND-ed with websearch_to_tsquery('simple', ...) (bare words and "phrases").
	WebTerms []string
	// PrefixQuery is an already-sanitised to_tsquery('simple', ...) expression (last bare token, `:*`).
	PrefixQuery   string
	From          []string // OR-ed substring matches on from_addr / from_name
	To            []string // OR-ed substring matches on the recipient addresses
	HasAttachment bool
	BusinessID    *uuid.UUID
	Cursor        *Cursor // results strictly after this (received_at, id) position
	Limit         int
}

// likeEscape escapes LIKE wildcards in s.
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// SearchMessages returns stored messages (archived included) matching p, newest first.
func (q *Queries) SearchMessages(ctx context.Context, p SearchParams) ([]Message, error) {
	if p.Limit <= 0 {
		p.Limit = 100
	}
	var (
		where []string
		args  []any
	)
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	where = append(where, "m.state='stored'")
	var ftsConds []string
	for _, t := range p.WebTerms {
		ftsConds = append(ftsConds, "s.fts @@ websearch_to_tsquery('simple', "+arg(t)+")")
	}
	if p.PrefixQuery != "" {
		ftsConds = append(ftsConds, "s.fts @@ to_tsquery('simple', "+arg(p.PrefixQuery)+")")
	}
	if len(ftsConds) > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM message_search s WHERE s.message_id=m.id AND "+strings.Join(ftsConds, " AND ")+")")
	}
	if len(p.From) > 0 {
		var or []string
		for _, f := range p.From {
			a := arg("%" + likeEscape(f) + "%")
			or = append(or, "m.from_addr ILIKE "+a, "m.from_name ILIKE "+a)
		}
		where = append(where, "("+strings.Join(or, " OR ")+")")
	}
	if len(p.To) > 0 {
		var or []string
		for _, f := range p.To {
			a := arg("%" + likeEscape(f) + "%")
			or = append(or, "m.env_to ILIKE "+a, "array_to_string(m.env_to_all, ' ') ILIKE "+a)
		}
		where = append(where, "("+strings.Join(or, " OR ")+")")
	}
	if p.HasAttachment {
		where = append(where, "m.attachment_count > 0")
	}
	if p.BusinessID != nil {
		where = append(where, "m.business_id="+arg(*p.BusinessID))
	}
	if p.Cursor != nil {
		where = append(where, "(m.received_at, m.id) < ("+arg(p.Cursor.ReceivedAt)+"::timestamptz, "+arg(p.Cursor.ID)+"::uuid)")
	}
	lim := arg(p.Limit)
	rows, err := q.db.Query(ctx, `SELECT `+messageCols+` FROM messages m WHERE `+strings.Join(where, " AND ")+
		` ORDER BY m.received_at DESC, m.id DESC LIMIT `+lim, args...)
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
