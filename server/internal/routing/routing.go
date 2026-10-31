// Package routing applies the rules engine to stored messages: it decides a route, records it
// and queues the Gmail write-back wants. It is shared by ingest and the rules API.
package routing

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/rules"
	"github.com/casprine/inbox/server/internal/store"
)

// GmailDelay is how long after routing the write-back becomes due.
const GmailDelay = 30 * time.Second

const batchSize = 500

// Outcome is the routing decision for one message.
type Outcome struct {
	Route  string // routed | unrouted
	RuleID *uuid.UUID
	Rule   *rules.Rule
}

// RulesMsg projects a stored message onto what the matcher sees.
func RulesMsg(m store.Message) rules.Msg {
	r := rules.Msg{BusinessID: m.BusinessID}
	if m.ToLocal != nil {
		r.ToLocal = *m.ToLocal
	}
	if m.FromAddr != nil {
		r.From = *m.FromAddr
	}
	if m.Subject != nil {
		r.Subject = *m.Subject
	}
	return r
}

// Evaluate decides the route for m against set.
func Evaluate(set *rules.Set, m store.Message) Outcome {
	if r, ok := set.First(RulesMsg(m)); ok {
		id := r.ID
		return Outcome{Route: "routed", RuleID: &id, Rule: &r}
	}
	return Outcome{Route: "unrouted"}
}

// Apply stores the decision for m and, for a routed message whose rule has Gmail actions,
// upserts the gmail_sync wants. m's business is kept: unrouted mail stays under its business.
func Apply(ctx context.Context, q *store.Queries, m store.Message, o Outcome) error {
	if err := q.SetMessageRoute(ctx, m.ID, m.BusinessID, o.Route, o.RuleID); err != nil {
		return err
	}
	return WantGmail(ctx, q, m.ID, o)
}

// WantGmail queues the rule's effective Gmail actions for a routed message.
func WantGmail(ctx context.Context, q *store.Queries, id uuid.UUID, o Outcome) error {
	if o.Rule == nil {
		return nil
	}
	a := o.Rule.Actions()
	if !a.Any() {
		return nil
	}
	var label *string
	if a.Label != "" {
		label = &a.Label
	}
	return q.WantGmail(ctx, id, label, a.Archive, a.MarkRead, GmailDelay)
}

// Reroute re-evaluates stored inbound messages against the current rules and returns how many
// ended up routed to a rule. unroutedOnly limits the pass to route='unrouted' messages, so
// existing assignments are never disturbed. Each batch commits on its own.
func Reroute(ctx context.Context, st *store.Store, unroutedOnly bool) (routed int, err error) {
	rs, err := st.ListRules(ctx)
	if err != nil {
		return 0, err
	}
	set := rules.NewSet(rs)
	after := uuid.Nil
	for {
		batch, err := st.RuleMessages(ctx, unroutedOnly, after, batchSize)
		if err != nil {
			return routed, err
		}
		if len(batch) == 0 {
			return routed, nil
		}
		err = st.WithTx(ctx, func(q *store.Queries) error {
			for _, m := range batch {
				o := Evaluate(set, m)
				if o.Route == "routed" {
					routed++
				}
				if o.Route == m.Route && sameRule(o.RuleID, m.RuleID) {
					continue
				}
				if err := Apply(ctx, q, m, o); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return routed, err
		}
		after = batch[len(batch)-1].ID
	}
}

func sameRule(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
