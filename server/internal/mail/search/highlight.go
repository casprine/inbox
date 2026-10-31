package search

import (
	"strings"
	"unicode"
)

// Span is a half-open range of rune offsets into the highlighted string.
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type tok struct {
	lower      string
	start, end int
}

func tokens(s string) []tok {
	var out []tok
	rs := []rune(s)
	i := 0
	for i < len(rs) {
		if !unicode.IsLetter(rs[i]) && !unicode.IsDigit(rs[i]) {
			i++
			continue
		}
		j := i
		for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j])) {
			j++
		}
		out = append(out, tok{strings.ToLower(string(rs[i:j])), i, j})
		i = j
	}
	return out
}

// Highlights returns rune-offset spans in text that match the free-text part of q: whole-word
// matches for bare words, the prefix of the last word, and token sequences for phrases.
// Spans are sorted and non-overlapping. Offsets are in runes so Swift/Kotlin clients can slice.
func (q Query) Highlights(text string) []Span {
	if text == "" || (len(q.Words) == 0 && len(q.Phrases) == 0 && q.Prefix == "") {
		return nil
	}
	ts := tokens(text)
	mark := make([]bool, len(ts))
	spans := map[int]int{} // token index -> end token index (inclusive)
	for _, w := range q.Words {
		runs := alnumRuns(w)
		for i := range ts {
			if matchSeq(ts, i, runs, false) {
				spans[i] = i + len(runs) - 1
			}
		}
	}
	for _, p := range q.Phrases {
		runs := alnumRuns(p)
		for i := range ts {
			if len(runs) > 0 && matchSeq(ts, i, runs, false) {
				spans[i] = i + len(runs) - 1
			}
		}
	}
	if runs := alnumRuns(q.Prefix); len(runs) > 0 {
		for i := range ts {
			if matchSeq(ts, i, runs, true) {
				spans[i] = i + len(runs) - 1
			}
		}
	}
	var out []Span
	for i := range ts {
		e, ok := spans[i]
		if !ok || mark[i] {
			continue
		}
		for k := i; k <= e; k++ {
			mark[k] = true
		}
		out = append(out, Span{ts[i].start, ts[e].end})
	}
	return out
}

// matchSeq reports whether ts[i:] starts with runs; with prefix the last run may be a prefix.
func matchSeq(ts []tok, i int, runs []string, prefix bool) bool {
	if len(runs) == 0 || i+len(runs) > len(ts) {
		return false
	}
	for k, r := range runs {
		t := ts[i+k].lower
		if prefix && k == len(runs)-1 {
			if !strings.HasPrefix(t, r) {
				return false
			}
		} else if t != r {
			return false
		}
	}
	return true
}

// Terms lists the lowercase terms a client can highlight on its own (words, phrase words, prefix).
func (q Query) Terms() []string {
	var out []string
	for _, w := range q.Words {
		out = append(out, alnumRuns(w)...)
	}
	for _, p := range q.Phrases {
		out = append(out, alnumRuns(p)...)
	}
	out = append(out, alnumRuns(q.Prefix)...)
	return out
}
