// Package rules is the pure rules engine: model, matcher, validator, evaluator and tester.
// It has no I/O; storage lives in internal/store and wiring in internal/routing.
package rules

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Rule is a routing rule. Lower Position wins; the first enabled matching rule routes a message.
type Rule struct {
	ID                 uuid.UUID
	Name               string
	Position           int
	Enabled            bool
	BusinessID         *uuid.UUID // nil = any business
	MatchToLocal       *string    // "invoices", "inv*", "*" or nil = any
	MatchFrom          *string    // "a@b.com", "@b.com" or nil = anyone
	MatchSubject       *string    // RE2, case-insensitive, nil = any
	GmailLabel         *string
	GmailArchive       bool
	GmailMarkRead      bool
	LabelOnly          bool
	ImportedFromFilter *string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Msg is the part of a message the matcher looks at.
type Msg struct {
	BusinessID *uuid.UUID
	ToLocal    string // local part of the recipient; a plus-tag, if present, is ignored
	From       string // sender address
	Subject    string
}

// GmailActions is what a matched rule asks Gmail to do.
type GmailActions struct {
	Label    string
	Archive  bool
	MarkRead bool
}

// Any reports whether there is anything to write back.
func (a GmailActions) Any() bool { return a.Label != "" || a.Archive || a.MarkRead }

// Actions returns the effective Gmail actions: the label always applies, archive and
// mark-read are suppressed when the rule is label-only.
func (r Rule) Actions() GmailActions {
	a := GmailActions{}
	if r.GmailLabel != nil {
		a.Label = strings.TrimSpace(*r.GmailLabel)
	}
	if !r.LabelOnly {
		a.Archive, a.MarkRead = r.GmailArchive, r.GmailMarkRead
	}
	return a
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

// StripTag lower-cases a local part and drops a "+tag" suffix.
func StripTag(local string) string {
	local = strings.ToLower(strings.TrimSpace(local))
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = strings.TrimSpace(local[:i])
	}
	return local
}

// glob matches s against a pattern where '*' matches any run of characters.
func glob(pat, s string) bool {
	if !strings.Contains(pat, "*") {
		return pat == s
	}
	parts := strings.Split(pat, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, last)
}

// Matcher is a rule compiled for repeated matching. A rule whose subject pattern does not
// compile never matches.
type Matcher struct {
	Rule    Rule
	to      string
	from    string
	subject *regexp.Regexp
	broken  bool
}

// Compile prepares r for matching.
func Compile(r Rule) *Matcher {
	m := &Matcher{Rule: r, to: StripTag(str(r.MatchToLocal)), from: strings.ToLower(str(r.MatchFrom))}
	if r.MatchSubject != nil && strings.TrimSpace(*r.MatchSubject) != "" {
		s := *r.MatchSubject // not trimmed: leading/trailing spaces are part of the pattern
		re, err := regexp.Compile("(?i)" + s)
		if err != nil {
			m.broken = true
		} else {
			m.subject = re
		}
	}
	return m
}

// Match reports whether the rule applies to msg, ignoring Enabled.
func (m *Matcher) Match(msg Msg) bool {
	if m.broken {
		return false
	}
	r := m.Rule
	if r.BusinessID != nil && (msg.BusinessID == nil || *msg.BusinessID != *r.BusinessID) {
		return false
	}
	if m.to != "" && m.to != "*" && !glob(m.to, StripTag(msg.ToLocal)) {
		return false
	}
	if m.from != "" {
		from := strings.ToLower(strings.TrimSpace(msg.From))
		if strings.HasPrefix(m.from, "@") {
			at := strings.LastIndexByte(from, '@')
			if at < 0 || from[at:] != m.from {
				return false
			}
		} else if from != m.from {
			return false
		}
	}
	if m.subject != nil && !m.subject.MatchString(msg.Subject) {
		return false
	}
	return true
}

// Set is an ordered list of enabled, compiled rules (first match wins).
type Set struct{ ms []*Matcher }

// NewSet keeps the enabled rules, ordered by Position (stable on input order).
func NewSet(rs []Rule) *Set {
	var en []Rule
	for _, r := range rs {
		if r.Enabled {
			en = append(en, r)
		}
	}
	sort.SliceStable(en, func(i, j int) bool { return en[i].Position < en[j].Position })
	s := &Set{}
	for _, r := range en {
		s.ms = append(s.ms, Compile(r))
	}
	return s
}

// First returns the first rule matching msg.
func (s *Set) First(msg Msg) (Rule, bool) {
	for _, m := range s.ms {
		if m.Match(msg) {
			return m.Rule, true
		}
	}
	return Rule{}, false
}

// Len is the number of enabled rules.
func (s *Set) Len() int { return len(s.ms) }
