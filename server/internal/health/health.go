// Package health turns the system's recent behaviour into the checks shown on the Health screen.
package health

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/cloudflare"
	"github.com/casprine/inbox/server/internal/gmail"
	"github.com/casprine/inbox/server/internal/store"
)

// Check states. "off" means the part is not set up; it never counts against the overall state.
const (
	OK    = "ok"
	Warn  = "warn"
	Error = "error"
	Off   = "off"
)

// Check is one row of the Health screen.
type Check struct {
	ID         string     `json:"id"`
	BusinessID *uuid.UUID `json:"business_id"`
	Title      string     `json:"title"`
	State      string     `json:"state"`
	Message    string     `json:"message"`
	// FixRoute is an app route id that opens the screen that fixes it: "gmail",
	// "business/<id>/routing" or "alfather". Null when there is nothing to open.
	FixRoute *string `json:"fix_route"`
}

// Report is the whole screen.
type Report struct {
	State     string    `json:"state"` // ok | needs_you
	CheckedAt time.Time `json:"checked_at"`
	Checks    []Check   `json:"checks"`
}

// Checker computes reports.
type Checker struct {
	Store *store.Store
	Gmail *gmail.Service // optional
	Now   func() time.Time
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func fix(route string) *string { return &route }

// Run evaluates every check.
func (c *Checker) Run(ctx context.Context) (Report, error) {
	now := c.now()
	biz, err := c.Store.BusinessActivities(ctx, now)
	if err != nil {
		return Report{}, err
	}
	var checks []Check
	checks = append(checks, mailArriving(biz, now)...)
	worker, err := c.worker(ctx, biz, now)
	if err != nil {
		return Report{}, err
	}
	checks = append(checks, worker)
	checks = append(checks, c.gmailCheck(ctx))
	q, err := c.queue(ctx, now)
	if err != nil {
		return Report{}, err
	}
	checks = append(checks, q)
	al, err := c.alfather(ctx, now)
	if err != nil {
		return Report{}, err
	}
	checks = append(checks, al)

	state := "ok"
	for _, ch := range checks {
		if ch.State == Warn || ch.State == Error {
			state = "needs_you"
		}
	}
	return Report{State: state, CheckedAt: now, Checks: checks}, nil
}

// quietThreshold is max(3 days, 3 x median gap): busy domains flag fast, quiet ones do not cry wolf.
func quietThreshold(medianSecs *float64) time.Duration {
	t := 72 * time.Hour
	if medianSecs != nil {
		if m := time.Duration(3 * *medianSecs * float64(time.Second)); m > t {
			t = m
		}
	}
	return t
}

func mailArriving(biz []store.BusinessActivity, now time.Time) []Check {
	var out []Check
	for _, b := range biz {
		id := b.ID
		ch := Check{ID: "mail_arriving", BusinessID: &id, Title: "Mail arriving · " + b.Name}
		route := fix("business/" + id.String() + "/routing")
		switch b.Routing {
		case cloudflare.RoutingToEmail:
			ch.State, ch.Message, ch.FixRoute = Error, b.Domain+" sends straight to email again, so Inbox isn't getting it", route
		case cloudflare.RoutingNone:
			ch.State, ch.Message, ch.FixRoute = Error, "Email routing is switched off for "+b.Domain, route
		default:
			last := b.CreatedAt
			if b.LastMessageAt != nil {
				last = *b.LastMessageAt
			}
			if quiet := now.Sub(last); quiet > quietThreshold(b.MedianGapSecs) {
				ch.State, ch.Message = Warn, "No mail for "+plural(quiet)
			} else if b.LastMessageAt == nil {
				ch.State, ch.Message = OK, "Waiting for the first message"
			} else {
				ch.State, ch.Message = OK, "Mail is arriving"
			}
		}
		out = append(out, ch)
	}
	return out
}

func (c *Checker) worker(ctx context.Context, biz []store.BusinessActivity, now time.Time) (Check, error) {
	ch := Check{ID: "worker", Title: "Mail worker"}
	st, err := c.Store.IngestStats(ctx, now)
	if err != nil {
		return ch, err
	}
	routed := false
	broken := false
	for _, b := range biz {
		switch b.Routing {
		case cloudflare.RoutingOK:
			routed = true
		case cloudflare.RoutingToEmail, cloudflare.RoutingNone:
			broken = true
		}
	}
	switch {
	case len(biz) == 0:
		ch.State, ch.Message = Off, "Add a business to start receiving mail"
	case st.ErrorsLast24h > 0:
		ch.State, ch.Message = Warn, fmt.Sprintf("Running, %d %s today", st.ErrorsLast24h, noun(st.ErrorsLast24h, "error", "errors"))
	case st.LastReceivedAt != nil && routed && !broken && now.Sub(*st.LastReceivedAt) > silentAfter(st.MedianGapSecs):
		ch.State, ch.Message = Warn, "Nothing has come through for "+plural(now.Sub(*st.LastReceivedAt))+" although routing looks right"
	default:
		ch.State, ch.Message = OK, "Running"
	}
	return ch, nil
}

func silentAfter(medianSecs *float64) time.Duration {
	t := 24 * time.Hour
	if medianSecs != nil {
		if m := time.Duration(6 * *medianSecs * float64(time.Second)); m > t {
			t = m
		}
	}
	return t
}

func (c *Checker) gmailCheck(ctx context.Context) Check {
	ch := Check{ID: "gmail", Title: "Gmail"}
	if c.Gmail == nil || !c.Gmail.Configured() {
		ch.State, ch.Message = Off, "Gmail isn't set up on this server"
		return ch
	}
	st, err := c.Gmail.Status(ctx)
	if err != nil {
		ch.State, ch.Message = Warn, "Couldn't read Gmail status"
		return ch
	}
	switch {
	case st.State == "not_connected":
		ch.State, ch.Message, ch.FixRoute = Off, "Not connected", fix("gmail")
	case st.State == "needs_reauth":
		ch.State, ch.FixRoute = Error, fix("gmail")
		ch.Message = "Signed out"
		if st.Waiting > 0 {
			ch.Message += fmt.Sprintf(", %d %s waiting", st.Waiting, noun(st.Waiting, "action", "actions"))
		}
	case st.RateLimitedUntil != nil:
		ch.State, ch.Message = Warn, "Paused by Gmail rate limits"
	default:
		ch.State, ch.Message = OK, "Connected"
		if st.Email != nil {
			ch.Message = "Connected as " + *st.Email
		}
	}
	return ch
}

func (c *Checker) queue(ctx context.Context, now time.Time) (Check, error) {
	ch := Check{ID: "queue", Title: "Background jobs"}
	s, err := c.Store.QueueStats(ctx, now)
	if err != nil {
		return ch, err
	}
	waiting := s.Available + s.Retryable
	switch {
	case s.OldestAvailable != nil && now.Sub(*s.OldestAvailable) > 15*time.Minute:
		ch.State, ch.Message = Error, "Stuck: the oldest job has waited "+plural(now.Sub(*s.OldestAvailable))
	case s.DiscardedLast24 > 0:
		ch.State, ch.Message = Warn, fmt.Sprintf("%d %s failed in the last day", s.DiscardedLast24, noun(s.DiscardedLast24, "job", "jobs"))
	case waiting > 20:
		ch.State, ch.Message = Warn, fmt.Sprintf("%d jobs waiting", waiting)
	default:
		ch.State, ch.Message = OK, "Nothing waiting"
	}
	return ch, nil
}

func (c *Checker) alfather(ctx context.Context, now time.Time) (Check, error) {
	ch := Check{ID: "alfather", Title: "Alfather"}
	a, err := c.Store.GetAlfather(ctx)
	if errors.Is(err, store.ErrNotFound) {
		ch.State, ch.Message = Off, "Not paired"
		return ch, nil
	}
	if err != nil {
		return ch, err
	}
	switch {
	case a.LastSeenAt != nil && now.Sub(*a.LastSeenAt) <= 5*time.Minute:
		ch.State, ch.Message = OK, fmt.Sprintf("Connected, %d waiting to be read", a.Waiting)
	case a.LastSeenAt != nil:
		ch.State, ch.Message, ch.FixRoute = Warn, "Not reachable · last seen "+plural(now.Sub(*a.LastSeenAt))+" ago", fix("alfather")
	default:
		ch.State, ch.Message, ch.FixRoute = Warn, "Paired but hasn't connected yet", fix("alfather")
	}
	return ch, nil
}

func noun(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// plural renders a duration in the largest sensible unit.
func plural(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d >= 2*time.Minute:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	return "a moment"
}
