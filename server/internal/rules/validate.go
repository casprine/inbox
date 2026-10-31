package rules

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode/utf8"
)

// FieldError is a validation failure on one field. Column is the 1-based character
// position inside the field's value when it can be located.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Column  *int   `json:"column,omitempty"`
}

func (e FieldError) Error() string { return e.Field + ": " + e.Message }

// IsPattern reports whether the error is about a match_* field.
func (e FieldError) IsPattern() bool { return strings.HasPrefix(e.Field, "match_") }

const (
	maxNameLen    = 100
	maxPatternLen = 500
	maxLabelLen   = 225
)

func col(n int) *int { return &n }

// Validate checks a rule's user-editable fields and returns all problems found.
func Validate(r Rule) []FieldError {
	var errs []FieldError
	if n := strings.TrimSpace(r.Name); n == "" {
		errs = append(errs, FieldError{Field: "name", Message: "Give the rule a name."})
	} else if utf8.RuneCountInString(n) > maxNameLen {
		errs = append(errs, FieldError{Field: "name", Message: fmt.Sprintf("Use at most %d characters.", maxNameLen)})
	}
	if r.MatchToLocal != nil {
		errs = append(errs, validateToLocal(*r.MatchToLocal)...)
	}
	if r.MatchFrom != nil {
		errs = append(errs, validateFrom(*r.MatchFrom)...)
	}
	if r.MatchSubject != nil {
		errs = append(errs, validateSubject(*r.MatchSubject)...)
	}
	if r.GmailLabel != nil {
		errs = append(errs, validateLabel(*r.GmailLabel)...)
	}
	return errs
}

func validateToLocal(v string) []FieldError {
	const f = "match_to_local"
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	for i, c := range []rune(v) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("._-+*%", c):
		default:
			return []FieldError{{Field: f, Message: fmt.Sprintf("%q is not allowed in the part before the @.", string(c)), Column: col(i + 1)}}
		}
	}
	if StripTag(v) == "" {
		return []FieldError{{Field: f, Message: "Enter the address name before any +tag.", Column: col(1)}}
	}
	return nil
}

func validateFrom(v string) []FieldError {
	const f = "match_from"
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if i := strings.IndexFunc(v, func(c rune) bool { return c <= ' ' || c == ',' || c == '<' || c == '>' }); i >= 0 {
		return []FieldError{{Field: f, Message: "Use one address (a@b.com) or a whole domain (@b.com).", Column: col(utf8.RuneCountInString(v[:i]) + 1)}}
	}
	at := strings.IndexByte(v, '@')
	if at < 0 {
		return []FieldError{{Field: f, Message: "Add the domain: a@b.com, or @b.com for everyone at a domain.", Column: col(utf8.RuneCountInString(v) + 1)}}
	}
	if j := strings.IndexByte(v[at+1:], '@'); j >= 0 {
		return []FieldError{{Field: f, Message: "Only one @ is allowed.", Column: col(utf8.RuneCountInString(v[:at+1+j]) + 1)}}
	}
	if at+1 == len(v) {
		return []FieldError{{Field: f, Message: "Add the domain after the @.", Column: col(utf8.RuneCountInString(v) + 1)}}
	}
	return nil
}

func validateSubject(v string) []FieldError {
	const f = "match_subject"
	if strings.TrimSpace(v) == "" {
		return nil
	}
	if utf8.RuneCountInString(v) > maxPatternLen {
		return []FieldError{{Field: f, Message: fmt.Sprintf("Use at most %d characters.", maxPatternLen), Column: col(maxPatternLen + 1)}}
	}
	// the case-insensitive flag never changes validity, so diagnose the user's own text
	_, err := regexp.Compile(v)
	if err == nil {
		_, err = regexp.Compile("(?i)" + v)
	}
	if err != nil {
		msg, c := "This pattern is not valid.", 1
		if se, ok := err.(*syntax.Error); ok {
			msg = fmt.Sprintf("Invalid pattern: %s near %q.", se.Code, se.Expr)
			if i := strings.Index(v, se.Expr); i >= 0 && se.Expr != "" {
				c = utf8.RuneCountInString(v[:i]) + 1
			}
		}
		return []FieldError{{Field: f, Message: msg, Column: col(c)}}
	}
	return nil
}

func validateLabel(v string) []FieldError {
	const f = "gmail_label"
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if utf8.RuneCountInString(v) > maxLabelLen {
		return []FieldError{{Field: f, Message: fmt.Sprintf("Use at most %d characters.", maxLabelLen), Column: col(maxLabelLen + 1)}}
	}
	for i, c := range []rune(v) {
		if c < ' ' || c == 0x7f {
			return []FieldError{{Field: f, Message: "Labels cannot contain control characters.", Column: col(i + 1)}}
		}
	}
	if strings.HasPrefix(v, "/") || strings.HasSuffix(v, "/") || strings.Contains(v, "//") {
		return []FieldError{{Field: f, Message: "Nested labels use single slashes between names.", Column: col(1)}}
	}
	return nil
}
