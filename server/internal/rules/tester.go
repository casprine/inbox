package rules

import (
	"sort"

	"github.com/google/uuid"
)

// Scope selects which stored messages a test or apply considers.
const (
	ScopeAll      = "all"
	ScopeUnrouted = "unrouted"
)

// TestMsg is a message offered to the tester.
type TestMsg struct {
	ID uuid.UUID
	Msg
}

// RuleRef names the rule that got there first.
type RuleRef struct {
	ID   uuid.UUID
	Name string
}

// CaughtEarlier is a message the draft matches but an earlier rule claims.
type CaughtEarlier struct {
	MessageID uuid.UUID
	By        RuleRef
}

// TestResult is the outcome of testing a draft against messages.
type TestResult struct {
	WouldMatch    []uuid.UUID
	CaughtEarlier []CaughtEarlier
}

// Test evaluates draft against msgs in the context of the existing rules. A draft whose ID
// is an existing rule replaces it in place; otherwise it is placed above all existing rules.
// The draft is treated as enabled. WouldMatch lists messages the draft would claim;
// CaughtEarlier lists messages it matches but a rule ahead of it claims first.
func Test(draft Rule, existing []Rule, msgs []TestMsg) TestResult {
	draft.Enabled = true
	ordered := append([]Rule(nil), existing...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })
	merged := make([]Rule, 0, len(ordered)+1)
	replaced := false
	for _, r := range ordered {
		if draft.ID != uuid.Nil && r.ID == draft.ID {
			merged = append(merged, draft)
			replaced = true
			continue
		}
		merged = append(merged, r)
	}
	if !replaced {
		if draft.ID == uuid.Nil {
			draft.ID = uuid.New()
		}
		merged = append([]Rule{draft}, merged...)
	}
	// positions follow slice order so ties in stored positions cannot reorder anything
	for i := range merged {
		merged[i].Position = i
	}
	set := NewSet(merged)
	dm := Compile(draft)
	res := TestResult{WouldMatch: []uuid.UUID{}, CaughtEarlier: []CaughtEarlier{}}
	for _, m := range msgs {
		if !dm.Match(m.Msg) {
			continue
		}
		win, ok := set.First(m.Msg)
		switch {
		case ok && win.ID == draft.ID:
			res.WouldMatch = append(res.WouldMatch, m.ID)
		case ok:
			res.CaughtEarlier = append(res.CaughtEarlier, CaughtEarlier{MessageID: m.ID, By: RuleRef{ID: win.ID, Name: win.Name}})
		}
	}
	return res
}
