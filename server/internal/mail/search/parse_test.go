package search

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Query
	}{
		{"", Query{}},
		{"   \t ", Query{}},
		{"northgate", Query{Prefix: "northgate"}},
		{"northgate ", Query{Words: []string{"northgate"}}},
		{"invoice northgate", Query{Words: []string{"invoice"}, Prefix: "northgate"}},
		{"invoice northgate  ", Query{Words: []string{"invoice", "northgate"}}},
		{"from:alice", Query{From: []string{"alice"}}},
		{"FROM:Alice@Example.com", Query{From: []string{"Alice@Example.com"}}},
		{"from:alice from:bob", Query{From: []string{"alice", "bob"}}},
		{"to:orders", Query{To: []string{"orders"}}},
		{"to:orders@acme.com invoice", Query{To: []string{"orders@acme.com"}, Prefix: "invoice"}},
		{"has:attachment", Query{HasAttachment: true}},
		{"HAS:Attachment report", Query{HasAttachment: true, Prefix: "report"}},
		{"has:attachments", Query{HasAttachment: true}},
		{"has:star", Query{Prefix: "has:star"}},
		{"has:star ", Query{Words: []string{"has:star"}}},
		{`"quarterly report"`, Query{Phrases: []string{"quarterly report"}}},
		{`"quarterly report" budget`, Query{Phrases: []string{"quarterly report"}, Prefix: "budget"}},
		{`budget "quarterly report"`, Query{Words: []string{"budget"}, Phrases: []string{"quarterly report"}}},
		{`"unterminated phrase`, Query{Phrases: []string{"unterminated phrase"}}},
		{`""`, Query{}},
		{`from:"John Smith"`, Query{From: []string{"John Smith"}}},
		{`from:"John Smith" invoice`, Query{From: []string{"John Smith"}, Prefix: "invoice"}},
		{`from:"unterminated name`, Query{From: []string{"unterminated name"}}},
		{"from:", Query{}},
		{"from: alice", Query{Prefix: "alice"}},
		{"to:", Query{}},
		{"has:", Query{}},
		{"foo:bar", Query{Prefix: "foo:bar"}},
		{"from:alice has:attachment to:orders tax return", Query{From: []string{"alice"}, To: []string{"orders"}, HasAttachment: true, Words: []string{"tax"}, Prefix: "return"}},
		{"from:alice invoice ", Query{From: []string{"alice"}, Words: []string{"invoice"}}},
		{"!!! ???", Query{}},
		{"invoice !!!", Query{Words: []string{"invoice"}}},
		{"naïve café", Query{Words: []string{"naïve"}, Prefix: "café"}},
		{"日本語 テスト", Query{Words: []string{"日本語"}, Prefix: "テスト"}},
		{`from:alice"x"`, Query{From: []string{"alice"}, Phrases: []string{"x"}}},
		{"a@b.com", Query{Prefix: "a@b.com"}},
	}
	for _, c := range cases {
		got := Parse(c.in)
		if !reflect.DeepEqual(norm(got), norm(c.want)) {
			t.Errorf("Parse(%q)\n got  %+v\n want %+v", c.in, got, c.want)
		}
	}
}

func norm(q Query) Query {
	z := func(s []string) []string {
		if len(s) == 0 {
			return nil
		}
		return s
	}
	q.Words, q.Phrases, q.From, q.To = z(q.Words), z(q.Phrases), z(q.From), z(q.To)
	return q
}

func TestEmpty(t *testing.T) {
	for in, want := range map[string]bool{"": true, "from:": true, "!!!": true, `""`: true, "x": false, "has:attachment": false, "from:a": false} {
		if got := Parse(in).Empty(); got != want {
			t.Errorf("Empty(%q)=%v", in, got)
		}
	}
}

func TestTSPrefixQuery(t *testing.T) {
	for in, want := range map[string]string{
		"north":     "'north':*",
		"North":     "'north':*",
		"foo-bar":   "'foo' & 'bar':*",
		"a@b.co":    "'a' & 'b' & 'co':*",
		"it's":      "'it' & 's':*",
		"x'; DROP":  "'x' & 'drop':*",
		"'":         "",
		"":          "",
		":*&|!()<>": "",
		"naïve":     "'naïve':*",
	} {
		if got := (Query{Prefix: in}).TSPrefixQuery(); got != want {
			t.Errorf("TSPrefixQuery(%q)=%q want %q", in, got, want)
		}
	}
}

func TestWebTerms(t *testing.T) {
	q := Parse(`-spam or "big deal" "  " acme.com `)
	got := q.WebTerms()
	want := []string{"spam", "acme.com", `"big deal"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v want %v", got, want)
	}
	if got := Parse("or ").WebTerms(); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestHighlights(t *testing.T) {
	cut := func(s string, sp []Span) []string {
		rs := []rune(s)
		var out []string
		for _, x := range sp {
			out = append(out, string(rs[x.Start:x.End]))
		}
		return out
	}
	type tc struct {
		q, text string
		want    []string
	}
	for _, c := range []tc{
		{"invoice", "Invoice for March", []string{"Invoice"}},
		{"inv", "Invoice for March", []string{"Invoice"}},
		{"inv ", "Invoice for March", nil}, // trailing space: exact word
		{"invoice ", "Invoice for March", []string{"Invoice"}},
		{`"for march"`, "Invoice for March", []string{"for March"}},
		{`"march for"`, "Invoice for March", nil},
		{"north", "Northgate and north star", []string{"Northgate", "north"}},
		{"north ", "Northgate and north star", []string{"north"}},
		{"café", "Grab a CAFÉ now", []string{"CAFÉ"}},
		{"日本", "これは日本語です 日本", []string{"日本"}},
		{"from:alice", "Alice", nil},
		{"", "text", nil},
		{"zzz", "", nil},
	} {
		got := cut(c.text, Parse(c.q).Highlights(c.text))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Highlights(%q,%q)=%v want %v", c.q, c.text, got, c.want)
		}
	}
	// rune offsets, not byte offsets
	sp := Parse("b ").Highlights("é b")
	if len(sp) != 1 || sp[0] != (Span{2, 3}) {
		t.Fatalf("%v", sp)
	}
}

func TestParseNeverPanics(t *testing.T) {
	for _, in := range []string{`"`, `""""`, `from:"`, `from:""`, `:`, `::`, `from::`, "\x00", "\xff\xfe", strings.Repeat(`"a `, 500), "to:\"a\"\"b\""} {
		q := Parse(in)
		_ = q.WebTerms()
		_ = q.TSPrefixQuery()
		_ = q.Highlights("some text a b")
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"from:a", `"x y" z`, "has:attachment q", `from:"a b`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q := Parse(s)
		_ = q.WebTerms()
		_ = q.TSPrefixQuery()
		_ = q.Highlights(s)
		if p := q.TSPrefixQuery(); strings.ContainsAny(strings.NewReplacer("':*", "", "'", "", " & ", "").Replace(p), "()|!<>:&*\\") {
			t.Fatalf("unsafe prefix %q from %q", p, s)
		}
	})
}

func TestAddRecent(t *testing.T) {
	var l []string
	for _, s := range []string{"a", "b", "A", "c"} {
		l = AddRecent(l, s)
	}
	if !reflect.DeepEqual(l, []string{"c", "A", "b"}) {
		t.Fatal(l)
	}
	for i := 0; i < 20; i++ {
		l = AddRecent(l, string(rune('a'+i))+"x")
	}
	if len(l) != 10 || l[0] != "tx" {
		t.Fatal(l)
	}
}
