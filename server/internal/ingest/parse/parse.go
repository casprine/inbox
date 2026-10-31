// Package parse turns raw RFC 5322 messages into a structured form and
// derives thread keys. All functions are pure (no I/O).
package parse

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jhillyerd/enmime/v2"
	"golang.org/x/net/html"
)

// SnippetLen is the maximum snippet length in runes.
const SnippetLen = 200

// Address is a parsed mailbox.
type Address struct {
	Name string
	Addr string // lower-cased addr-spec
}

// Attachment is a non-body MIME part.
type Attachment struct {
	Index     int // position among all attachments/inlines, in document order
	Filename  string
	MIME      string
	Size      int
	ContentID string // without angle brackets
	Inline    bool
	content   []byte
}

// Bytes returns the decoded content.
func (a *Attachment) Bytes() []byte { return a.content }

// AuthResults holds verdicts from Authentication-Results; empty if absent.
type AuthResults struct {
	SPF, DKIM, DMARC string
}

// Parsed is the structured form of a message.
type Parsed struct {
	MessageID   string // without <>, or "sha256:<hex>" fallback
	HasRealID   bool
	From        Address
	To, Cc      []Address
	DeliveredTo []string // Delivered-To / X-Original-To / Envelope-To candidates, lower-cased
	Subject     string
	Date        time.Time // zero if missing/unparseable
	InReplyTo   string
	References  []string
	Text, HTML  string
	Snippet     string
	Attachments []*Attachment
	Auth        AuthResults
	Warnings    []string // recoverable parse problems
}

// Parse parses raw. It is tolerant: recoverable problems become Warnings.
func Parse(raw []byte) (*Parsed, error) {
	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	p := &Parsed{}
	for _, e := range env.Errors {
		p.Warnings = append(p.Warnings, e.Error())
	}

	if id := cleanID(env.GetHeader("Message-ID")); id != "" {
		p.MessageID, p.HasRealID = id, true
	} else {
		sum := sha256.Sum256(raw)
		p.MessageID = "sha256:" + hex.EncodeToString(sum[:])
	}

	if fl := addrs(env, "From"); len(fl) > 0 {
		p.From = fl[0]
	}
	p.To = addrs(env, "To")
	p.Cc = addrs(env, "Cc")
	seen := map[string]bool{}
	for _, h := range []string{"Delivered-To", "X-Original-To", "Envelope-To"} {
		for _, v := range env.GetHeaderValues(h) {
			for _, part := range strings.Split(v, ",") {
				a := strings.ToLower(strings.Trim(strings.TrimSpace(part), "<>"))
				if a != "" && !seen[a] {
					seen[a] = true
					p.DeliveredTo = append(p.DeliveredTo, a)
				}
			}
		}
	}

	p.Subject = strings.TrimSpace(env.GetHeader("Subject"))
	if d, err := mail.ParseDate(strings.TrimSpace(env.GetHeader("Date"))); err == nil {
		p.Date = d
	}
	p.InReplyTo = cleanID(env.GetHeader("In-Reply-To"))
	p.References = splitIDs(env.GetHeader("References"))

	p.Text, p.HTML = env.Text, env.HTML
	if p.Text != "" {
		p.Snippet = snippet(p.Text)
	} else if p.HTML != "" {
		p.Snippet = snippet(htmlToText(p.HTML))
	}

	idx := 0
	add := func(part *enmime.Part, inline bool) {
		cid := strings.Trim(strings.TrimSpace(part.ContentID), "<>")
		p.Attachments = append(p.Attachments, &Attachment{
			Index: idx, Filename: part.FileName, MIME: part.ContentType,
			Size: len(part.Content), ContentID: cid, Inline: inline, content: part.Content,
		})
		idx++
	}
	for _, a := range env.Inlines {
		add(a, true)
	}
	for _, a := range env.Attachments {
		add(a, false)
	}
	for _, a := range env.OtherParts {
		add(a, true)
	}
	p.Auth = parseAuth(env.GetHeaderValues("Authentication-Results"))
	return p, nil
}

func addrs(env *enmime.Envelope, key string) []Address {
	list, err := env.AddressList(key)
	if err != nil && len(list) == 0 {
		return nil
	}
	out := make([]Address, 0, len(list))
	for _, a := range list {
		out = append(out, Address{Name: a.Name, Addr: strings.ToLower(a.Address)})
	}
	return out
}

func cleanID(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j > 0 {
			return strings.TrimSpace(s[i+1 : i+j])
		}
	}
	if strings.ContainsAny(s, " \t") {
		return ""
	}
	return strings.Trim(s, "<>")
}

var idRe = regexp.MustCompile(`<([^<>\s]+)>`)

func splitIDs(s string) []string {
	var out []string
	for _, m := range idRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

var wsRe = regexp.MustCompile(`\s+`)

func snippet(s string) string {
	s = strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
	if utf8.RuneCountInString(s) <= SnippetLen {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:SnippetLen]))
}

var blockTags = map[string]bool{"p": true, "div": true, "br": true, "li": true, "tr": true, "td": true,
	"th": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "blockquote": true, "table": true}

func htmlToText(src string) string {
	z := html.NewTokenizer(strings.NewReader(src))
	var b strings.Builder
	skip := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return b.String()
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			n := string(name)
			if n == "script" || n == "style" || n == "head" || n == "title" {
				skip++
			} else if blockTags[n] {
				b.WriteByte(' ')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			n := string(name)
			if n == "script" || n == "style" || n == "head" || n == "title" {
				if skip > 0 {
					skip--
				}
			} else if blockTags[n] {
				b.WriteByte(' ')
			}
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		}
	}
}

var authRe = regexp.MustCompile(`(?i)\b(spf|dkim|dmarc)\s*=\s*([a-z]+)`)

func parseAuth(headers []string) AuthResults {
	var r AuthResults
	for _, h := range headers {
		for _, m := range authRe.FindAllStringSubmatch(h, -1) {
			v := strings.ToLower(m[2])
			switch strings.ToLower(m[1]) {
			case "spf":
				if r.SPF == "" {
					r.SPF = v
				}
			case "dkim":
				if r.DKIM == "" {
					r.DKIM = v
				}
			case "dmarc":
				if r.DMARC == "" {
					r.DMARC = v
				}
			}
		}
	}
	return r
}

// SplitAddress splits an address into local part (without tag), plus-tag and
// lower-cased domain. "Foo+bar@X.com" -> ("foo", "bar", "x.com").
func SplitAddress(addr string) (local, tag, domain string) {
	addr = strings.ToLower(strings.TrimSpace(addr))
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return addr, "", ""
	}
	local, domain = addr[:at], addr[at+1:]
	if i := strings.Index(local, "+"); i >= 0 {
		local, tag = local[:i], local[i+1:]
	}
	return local, tag, domain
}

var subjPrefix = regexp.MustCompile(`(?i)^\s*(re|fwd?|aw|sv)\s*(\[\d+\])?\s*:\s*`)

// NormalizeSubject strips reply/forward prefixes, collapses whitespace and lower-cases.
func NormalizeSubject(s string) string {
	for {
		t := subjPrefix.ReplaceAllString(s, "")
		if t == s {
			break
		}
		s = t
	}
	return strings.ToLower(strings.TrimSpace(wsRe.ReplaceAllString(s, " ")))
}

// ThreadKey returns the thread key for p. lookup receives the referenced
// message ids (In-Reply-To first, then References newest-first) and returns
// the root key of an already-stored thread if any is known. Otherwise the key
// is "subj:" + sha1(normalised subject).
func ThreadKey(p *Parsed, lookup func(ids []string) (rootKey string, ok bool)) string {
	if lookup != nil {
		var ids []string
		seen := map[string]bool{}
		push := func(id string) {
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		push(p.InReplyTo)
		for i := len(p.References) - 1; i >= 0; i-- {
			push(p.References[i])
		}
		if len(ids) > 0 {
			if k, ok := lookup(ids); ok && k != "" {
				return k
			}
		}
	}
	sum := sha1.Sum([]byte(NormalizeSubject(p.Subject)))
	return "subj:" + hex.EncodeToString(sum[:])
}
