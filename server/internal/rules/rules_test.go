package rules

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
)

func sp(s string) *string { return &s }

func TestMatcher(t *testing.T) {
	biz := uuid.New()
	other := uuid.New()
	tests := []struct {
		name string
		rule Rule
		msg  Msg
		want bool
	}{
		{"empty rule matches all", Rule{}, Msg{ToLocal: "x"}, true},
		{"to exact", Rule{MatchToLocal: sp("invoices")}, Msg{ToLocal: "invoices"}, true},
		{"to case-insensitive", Rule{MatchToLocal: sp("Invoices")}, Msg{ToLocal: "INVOICES"}, true},
		{"to msg tag stripped", Rule{MatchToLocal: sp("invoices")}, Msg{ToLocal: "invoices+acme"}, true},
		{"to rule tag stripped", Rule{MatchToLocal: sp("invoices+acme")}, Msg{ToLocal: "invoices"}, true},
		{"to mismatch", Rule{MatchToLocal: sp("invoices")}, Msg{ToLocal: "billing"}, false},
		{"to star", Rule{MatchToLocal: sp("*")}, Msg{ToLocal: "anything"}, true},
		{"to star empty local", Rule{MatchToLocal: sp("*")}, Msg{}, true},
		{"to prefix glob", Rule{MatchToLocal: sp("inv*")}, Msg{ToLocal: "invoices+x"}, true},
		{"to suffix glob", Rule{MatchToLocal: sp("*-eu")}, Msg{ToLocal: "sales-eu"}, true},
		{"to middle glob miss", Rule{MatchToLocal: sp("a*c")}, Msg{ToLocal: "abd"}, false},
		{"to not substring", Rule{MatchToLocal: sp("invoice")}, Msg{ToLocal: "invoices"}, false},
		{"from exact", Rule{MatchFrom: sp("a@b.com")}, Msg{From: "A@B.com"}, true},
		{"from exact miss", Rule{MatchFrom: sp("a@b.com")}, Msg{From: "x@b.com"}, false},
		{"from domain", Rule{MatchFrom: sp("@b.com")}, Msg{From: "anyone@b.com"}, true},
		{"from domain case", Rule{MatchFrom: sp("@B.com")}, Msg{From: "anyone@b.COM"}, true},
		{"from domain not suffix", Rule{MatchFrom: sp("@b.com")}, Msg{From: "x@notb.com"}, false},
		{"from domain not subdomain", Rule{MatchFrom: sp("@b.com")}, Msg{From: "x@mail.b.com"}, false},
		{"from empty msg", Rule{MatchFrom: sp("@b.com")}, Msg{}, false},
		{"subject ci", Rule{MatchSubject: sp("^invoice #\\d+")}, Msg{Subject: "INVOICE #42 due"}, true},
		{"subject miss", Rule{MatchSubject: sp("^receipt")}, Msg{Subject: "your invoice"}, false},
		{"subject invalid never matches", Rule{MatchSubject: sp("(")}, Msg{Subject: "("}, false},
		{"business scoped hit", Rule{BusinessID: &biz}, Msg{BusinessID: &biz}, true},
		{"business scoped miss", Rule{BusinessID: &biz}, Msg{BusinessID: &other}, false},
		{"business scoped no biz", Rule{BusinessID: &biz}, Msg{}, false},
		{"unscoped rule, any biz", Rule{}, Msg{BusinessID: &other}, true},
		{"all conditions", Rule{BusinessID: &biz, MatchToLocal: sp("a"), MatchFrom: sp("@b.com"), MatchSubject: sp("hi")},
			Msg{BusinessID: &biz, ToLocal: "a+t", From: "z@b.com", Subject: "Oh HI"}, true},
		{"one condition fails", Rule{MatchToLocal: sp("a"), MatchFrom: sp("@b.com")}, Msg{ToLocal: "a", From: "z@c.com"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Compile(tc.rule).Match(tc.msg); got != tc.want {
				t.Fatalf("Match = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSetFirstMatchWins(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	rs := []Rule{
		{ID: c, Name: "c", Position: 2, Enabled: true},
		{ID: a, Name: "a", Position: 0, Enabled: false, MatchToLocal: sp("x")},
		{ID: b, Name: "b", Position: 1, Enabled: true, MatchToLocal: sp("x")},
	}
	s := NewSet(rs)
	if s.Len() != 2 {
		t.Fatalf("len = %d", s.Len())
	}
	if r, ok := s.First(Msg{ToLocal: "x"}); !ok || r.ID != b {
		t.Fatalf("got %v %v, want b", r.Name, ok)
	}
	if r, ok := s.First(Msg{ToLocal: "y"}); !ok || r.ID != c {
		t.Fatalf("got %v, want c", r.Name)
	}
	if _, ok := NewSet(nil).First(Msg{}); ok {
		t.Fatal("empty set matched")
	}
}

func TestActions(t *testing.T) {
	tests := []struct {
		name string
		r    Rule
		want GmailActions
	}{
		{"none", Rule{}, GmailActions{}},
		{"label only suppresses", Rule{GmailLabel: sp(" Biz "), GmailArchive: true, GmailMarkRead: true, LabelOnly: true}, GmailActions{Label: "Biz"}},
		{"full", Rule{GmailLabel: sp("Biz"), GmailArchive: true, GmailMarkRead: true}, GmailActions{Label: "Biz", Archive: true, MarkRead: true}},
		{"archive only", Rule{GmailArchive: true}, GmailActions{Archive: true}},
		{"label only no label", Rule{GmailArchive: true, LabelOnly: true}, GmailActions{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.Actions(); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
	if (GmailActions{}).Any() || !(GmailActions{Archive: true}).Any() {
		t.Fatal("Any")
	}
}

func intp(n int) *int { return &n }

func TestValidate(t *testing.T) {
	long := strings.Repeat("a", maxPatternLen+1)
	tests := []struct {
		name  string
		r     Rule
		field string
		col   *int
	}{
		{"ok", Rule{Name: "n", MatchToLocal: sp("inv*"), MatchFrom: sp("@b.com"), MatchSubject: sp(`^inv\d+`), GmailLabel: sp("A/B")}, "", nil},
		{"name required", Rule{Name: "  "}, "name", nil},
		{"unclosed paren", Rule{Name: "n", MatchSubject: sp("abc(def")}, "match_subject", intp(1)},
		{"leading star", Rule{Name: "n", MatchSubject: sp("*abc")}, "match_subject", intp(1)},
		{"bad escape", Rule{Name: "n", MatchSubject: sp(`ab\`)}, "match_subject", nil},
		{"lookahead unsupported", Rule{Name: "n", MatchSubject: sp("a(?=b)")}, "match_subject", intp(2)},
		{"too long", Rule{Name: "n", MatchSubject: sp(long)}, "match_subject", intp(maxPatternLen + 1)},
		{"to bad char", Rule{Name: "n", MatchToLocal: sp("a b")}, "match_to_local", intp(2)},
		{"to at sign", Rule{Name: "n", MatchToLocal: sp("a@b")}, "match_to_local", intp(2)},
		{"to only tag", Rule{Name: "n", MatchToLocal: sp("+x")}, "match_to_local", intp(1)},
		{"from no at", Rule{Name: "n", MatchFrom: sp("b.com")}, "match_from", nil},
		{"from two at", Rule{Name: "n", MatchFrom: sp("a@b@c")}, "match_from", intp(4)},
		{"from no domain", Rule{Name: "n", MatchFrom: sp("a@")}, "match_from", nil},
		{"from space", Rule{Name: "n", MatchFrom: sp("a @b.com")}, "match_from", intp(2)},
		{"label control", Rule{Name: "n", GmailLabel: sp("a\x01b")}, "gmail_label", intp(2)},
		{"label slash", Rule{Name: "n", GmailLabel: sp("a//b")}, "gmail_label", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := Validate(tc.r)
			if tc.field == "" {
				if len(errs) != 0 {
					t.Fatalf("unexpected %v", errs)
				}
				return
			}
			if len(errs) == 0 || errs[0].Field != tc.field {
				t.Fatalf("errs = %v, want field %s", errs, tc.field)
			}
			if errs[0].Message == "" || (errs[0].Column == nil && tc.field != "name") {
				t.Fatalf("missing message/column: %+v", errs[0])
			}
			if tc.col != nil && *errs[0].Column != *tc.col {
				t.Fatalf("column = %d, want %d (%s)", *errs[0].Column, *tc.col, errs[0].Message)
			}
		})
	}
}

func TestValidateMultiple(t *testing.T) {
	errs := Validate(Rule{Name: "", MatchSubject: sp("("), MatchFrom: sp("x")})
	if len(errs) != 3 {
		t.Fatalf("errs = %v", errs)
	}
}

func TestTester(t *testing.T) {
	r1, r2, r3 := uuid.New(), uuid.New(), uuid.New()
	existing := []Rule{
		{ID: r2, Name: "Invoices", Position: 1, Enabled: true, MatchSubject: sp("invoice")},
		{ID: r1, Name: "Acme", Position: 0, Enabled: true, MatchFrom: sp("@acme.com")},
		{ID: r3, Name: "Off", Position: 2, Enabled: false, MatchFrom: sp("@zzz.com")},
	}
	m := func(from, subj string) TestMsg {
		return TestMsg{ID: uuid.New(), Msg: Msg{From: from, Subject: subj, ToLocal: "billing"}}
	}
	acmeInv, otherInv, zzz, none := m("x@acme.com", "Invoice 1"), m("y@other.com", "Invoice 2"), m("q@zzz.com", "hello"), m("n@n.com", "hi")
	msgs := []TestMsg{acmeInv, otherInv, zzz, none}

	t.Run("new draft goes on top", func(t *testing.T) {
		res := Test(Rule{Name: "draft", MatchSubject: sp("invoice")}, existing, msgs)
		if len(res.WouldMatch) != 2 || len(res.CaughtEarlier) != 0 {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("edited draft keeps its position", func(t *testing.T) {
		res := Test(Rule{ID: r2, Name: "Invoices", MatchSubject: sp("invoice")}, existing, msgs)
		if len(res.WouldMatch) != 1 || res.WouldMatch[0] != otherInv.ID {
			t.Fatalf("would match %v", res.WouldMatch)
		}
		if len(res.CaughtEarlier) != 1 || res.CaughtEarlier[0].MessageID != acmeInv.ID || res.CaughtEarlier[0].By.ID != r1 || res.CaughtEarlier[0].By.Name != "Acme" {
			t.Fatalf("caught %+v", res.CaughtEarlier)
		}
	})
	t.Run("edited draft changes patterns", func(t *testing.T) {
		res := Test(Rule{ID: r1, Name: "Acme", MatchFrom: sp("@other.com")}, existing, msgs)
		if len(res.WouldMatch) != 1 || res.WouldMatch[0] != otherInv.ID || len(res.CaughtEarlier) != 0 {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("disabled existing is ignored, disabled draft is tested", func(t *testing.T) {
		res := Test(Rule{ID: r3, Name: "Off", Enabled: false, MatchFrom: sp("@zzz.com")}, existing, msgs)
		if len(res.WouldMatch) != 1 || res.WouldMatch[0] != zzz.ID {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("empty results are non-nil", func(t *testing.T) {
		res := Test(Rule{MatchSubject: sp("nomatch")}, nil, msgs)
		if res.WouldMatch == nil || res.CaughtEarlier == nil {
			t.Fatal("nil slices")
		}
	})
}

func FuzzMatcher(f *testing.F) {
	f.Add("inv*", "@b.com", "^a+", "Inv+x", "Z@b.com", "aaa")
	f.Add("*", "a@b.com", "(", "", "", "")
	f.Add("+", "@", "[", "a+b+c", "@@", "\xff")
	f.Fuzz(func(t *testing.T, to, from, subj, mToLocal, mFrom, mSubj string) {
		r := Rule{Name: "n", MatchToLocal: &to, MatchFrom: &from, MatchSubject: &subj}
		errs := Validate(r)
		m := Compile(r)
		got := m.Match(Msg{ToLocal: mToLocal, From: mFrom, Subject: mSubj})
		if len(errs) > 0 {
			for _, e := range errs {
				if e.Message == "" || e.Field == "" || (e.Column != nil && *e.Column < 1) {
					t.Fatalf("bad error %+v", e)
				}
			}
		}
		if m.broken && got {
			t.Fatal("broken rule matched")
		}
		for _, e := range errs {
			if e.Field == "match_subject" && !m.broken && utf8.ValidString(subj) && strings.TrimSpace(subj) != "" && len(subj) <= maxPatternLen {
				t.Fatalf("validator rejected compilable pattern %q: %v", subj, e)
			}
		}
		// plus-tags on the message never change the outcome
		if got != Compile(r).Match(Msg{ToLocal: mToLocal + "+tag", From: mFrom, Subject: mSubj}) && !strings.Contains(mToLocal, "+") {
			t.Fatal("tag changed match")
		}
		// an empty rule matches everything
		if !Compile(Rule{}).Match(Msg{ToLocal: mToLocal, From: mFrom, Subject: mSubj}) {
			t.Fatal("empty rule must match")
		}
	})
}

func FuzzValidate(f *testing.F) {
	f.Add("n", "a", "a@b.com", `\d+`, "L")
	f.Add("", "*", "@", "(?i)(", "//")
	f.Fuzz(func(t *testing.T, name, to, from, subj, label string) {
		errs := Validate(Rule{Name: name, MatchToLocal: &to, MatchFrom: &from, MatchSubject: &subj, GmailLabel: &label})
		if len(errs) == 0 {
			if Compile(Rule{MatchSubject: &subj}).broken {
				t.Fatalf("valid but not compilable: %q", subj)
			}
		}
	})
}
