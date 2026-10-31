// Package search parses the user's search string (operators, phrases, bare words) and
// serves GET /v1/search plus the recent-searches endpoints.
package search

import (
	"strings"
	"unicode"
)

// MaxQueryLen bounds the accepted q parameter (runes).
const MaxQueryLen = 200

// Query is the parsed form of a search string.
type Query struct {
	Words         []string // complete bare words (exact lexeme match)
	Phrases       []string // quoted phrases
	Prefix        string   // last bare word when it is still being typed (prefix match); "" if none
	From          []string // from: values, OR-ed
	To            []string // to: values, OR-ed
	HasAttachment bool     // has:attachment
}

// Empty reports whether the query constrains nothing.
func (q Query) Empty() bool {
	return len(q.Words) == 0 && len(q.Phrases) == 0 && q.Prefix == "" && len(q.From) == 0 && len(q.To) == 0 && !q.HasAttachment
}

type token struct {
	text   string // unquoted text (for operators: the whole "op:value")
	quoted bool   // the token (or its operator value) was quoted
	op     string // lowercased operator name, "" for bare/phrase tokens
	val    string // operator value
	trail  bool   // followed by whitespace (or is not the final token)
}

// Parse turns s into a Query. It never fails: unknown operators are treated as bare words,
// empty operator values are ignored (the user is mid-typing), an unterminated quote runs to the end.
//
// The last token becomes a prefix match when it is a plain bare word that is not followed by
// whitespace, so "northg" matches "Northgate" while typing but "northgate " is exact.
func Parse(s string) Query {
	var q Query
	toks := tokenize(s)
	for i, t := range toks {
		last := i == len(toks)-1
		switch {
		case t.op == "from":
			if t.val != "" {
				q.From = append(q.From, t.val)
			}
		case t.op == "to":
			if t.val != "" {
				q.To = append(q.To, t.val)
			}
		case t.op == "has":
			switch strings.ToLower(t.val) {
			case "attachment", "attachments":
				q.HasAttachment = true
			case "":
			default: // unknown has: value -> plain text
				if last && !t.trail {
					q.Prefix = t.text
				} else {
					q.Words = append(q.Words, t.text)
				}
			}
		case t.quoted:
			if strings.TrimSpace(t.text) != "" {
				q.Phrases = append(q.Phrases, strings.TrimSpace(t.text))
			}
		default:
			if !hasAlnum(t.text) {
				continue
			}
			if last && !t.trail {
				q.Prefix = t.text
			} else {
				q.Words = append(q.Words, t.text)
			}
		}
	}
	return q
}

func hasAlnum(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func isOp(name string) bool {
	switch strings.ToLower(name) {
	case "from", "to", "has":
		return true
	}
	return false
}

// tokenize splits on whitespace, honouring "double quotes" (also as an operator value).
func tokenize(s string) []token {
	rs := []rune(s)
	var out []token
	i := 0
	n := len(rs)
	for i < n {
		if unicode.IsSpace(rs[i]) {
			i++
			continue
		}
		if rs[i] == '"' {
			text, next, _ := readQuoted(rs, i)
			out = append(out, token{text: text, quoted: true, trail: next < n})
			i = next
			continue
		}
		start := i
		for i < n && !unicode.IsSpace(rs[i]) && rs[i] != '"' {
			i++
		}
		word := string(rs[start:i])
		// operator with a quoted value: from:"john smith"
		if c := strings.IndexByte(word, ':'); c > 0 && c == len(word)-1 && i < n && rs[i] == '"' && isOp(word[:c]) {
			text, next, _ := readQuoted(rs, i)
			out = append(out, token{text: word + text, quoted: true, op: strings.ToLower(word[:c]), val: strings.TrimSpace(text), trail: next < n})
			i = next
			continue
		}
		t := token{text: word, trail: i < n}
		if c := strings.IndexByte(word, ':'); c > 0 && isOp(word[:c]) {
			t.op, t.val = strings.ToLower(word[:c]), strings.TrimSpace(word[c+1:])
		}
		out = append(out, t)
	}
	return out
}

// readQuoted reads a quoted string starting at rs[i]=='"'. next is the index after the closing
// quote (or len(rs) if unterminated).
func readQuoted(rs []rune, i int) (text string, next int, closed bool) {
	j := i + 1
	for j < len(rs) && rs[j] != '"' {
		j++
	}
	if j < len(rs) {
		return string(rs[i+1 : j]), j + 1, true
	}
	return string(rs[i+1:]), len(rs), false
}

// alnumRuns splits s into maximal runs of letters/digits (lowercased).
func alnumRuns(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// TSPrefixQuery builds a to_tsquery('simple') expression for the prefix word:
// "foo-bar" -> `'foo' & 'bar':*`. Returns "" when the word has no searchable characters.
func (q Query) TSPrefixQuery() string {
	runs := alnumRuns(q.Prefix)
	if len(runs) == 0 {
		return ""
	}
	parts := make([]string, len(runs))
	for i, r := range runs {
		parts[i] = "'" + r + "'"
	}
	parts[len(parts)-1] += ":*"
	return strings.Join(parts, " & ")
}

// WebTerms are the bare words and phrases as websearch_to_tsquery inputs (AND-ed by the caller).
// Words keep their original spelling so compound tokens (hosts, emails) tokenize exactly as they
// were indexed; websearch syntax characters are neutralised. Phrases are double-quoted.
func (q Query) WebTerms() []string {
	var out []string
	for _, w := range q.Words {
		w = strings.TrimLeft(w, "-+")
		if !hasAlnum(w) || strings.EqualFold(w, "or") {
			continue
		}
		out = append(out, w)
	}
	for _, p := range q.Phrases {
		if hasAlnum(p) {
			out = append(out, `"`+strings.TrimSpace(p)+`"`)
		}
	}
	return out
}
