package ingest

import (
	"context"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/ingest/parse"
	"github.com/casprine/inbox/server/internal/routing"
	"github.com/casprine/inbox/server/internal/rules"
	"github.com/casprine/inbox/server/internal/store"
)

// Decision is a routing outcome for one message.
type Decision struct {
	BusinessID *uuid.UUID
	Route      string // pending | routed | unrouted
	RuleID     *uuid.UUID
}

// Router decides where a freshly stored message goes. RulesRouter is the
// implementation; q runs inside the ingest transaction.
type Router interface {
	Route(ctx context.Context, q *store.Queries, m store.Message, p *parse.Parsed) (Decision, error)
}

// RulesRouter routes with the stored rules: first enabled match wins. The message keeps the
// business resolved from its recipient domain whether or not a rule matched (Q-T06-1).
// For a routed message whose rule has Gmail actions it also queues the gmail_sync wants.
type RulesRouter struct{}

func (RulesRouter) Route(ctx context.Context, q *store.Queries, m store.Message, _ *parse.Parsed) (Decision, error) {
	rs, err := q.ListRules(ctx)
	if err != nil {
		return Decision{}, err
	}
	o := routing.Evaluate(rules.NewSet(rs), m)
	if err := routing.WantGmail(ctx, q, m.ID, o); err != nil {
		return Decision{}, err
	}
	return Decision{BusinessID: m.BusinessID, Route: o.Route, RuleID: o.RuleID}, nil
}
