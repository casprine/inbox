package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, name string) *Parsed {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return p
}

func TestParse(t *testing.T) {
	tests := []struct {
		file  string
		check func(t *testing.T, p *Parsed)
	}{
		{"plain.eml", func(t *testing.T, p *Parsed) {
			eq(t, p.MessageID, "plain-1@example.com")
			eq(t, p.From.Addr, "alice@example.com")
			eq(t, p.From.Name, "Alice Example")
			eq(t, len(p.To), 1)
			eq(t, p.To[0].Addr, "shop+orders@mydomain.com")
			eq(t, p.DeliveredTo[0], "shop+orders@mydomain.com")
			eq(t, p.Subject, "Hello there")
			eq(t, p.Date.UTC().Format("2006-01-02T15:04"), "2026-01-05T10:00")
			eq(t, p.Snippet, "Hi, This is a plain message.")
			eq(t, p.Auth, AuthResults{SPF: "pass", DKIM: "pass", DMARC: "pass"})
		}},
		{"alternative.eml", func(t *testing.T, p *Parsed) {
			eq(t, strings.TrimSpace(p.Text), "Plain digest text")
			if !strings.Contains(p.HTML, "HTML digest") {
				t.Errorf("html = %q", p.HTML)
			}
			eq(t, len(p.To), 2)
			eq(t, p.To[1].Name, "B Person")
			eq(t, p.Cc[0].Addr, "c@mydomain.com")
			eq(t, p.Auth, AuthResults{})
			eq(t, len(p.Attachments), 0)
		}},
		{"htmlonly.eml", func(t *testing.T, p *Parsed) {
			eq(t, p.Snippet, "Hello world & co")
		}},
		{"inline_cid.eml", func(t *testing.T, p *Parsed) {
			eq(t, len(p.Attachments), 1)
			a := p.Attachments[0]
			eq(t, a.ContentID, "logo123@example.org")
			eq(t, a.Inline, true)
			eq(t, a.MIME, "image/png")
			eq(t, a.Filename, "logo.png")
			eq(t, a.Size, len(a.Bytes()))
			if a.Size == 0 || string(a.Bytes()[1:4]) != "PNG" {
				t.Errorf("bad png bytes (size %d)", a.Size)
			}
		}},
		{"attachment.eml", func(t *testing.T, p *Parsed) {
			eq(t, len(p.Attachments), 1)
			a := p.Attachments[0]
			eq(t, a.Filename, "invoice.pdf")
			eq(t, a.MIME, "application/pdf")
			eq(t, a.Inline, false)
			eq(t, string(a.Bytes()), "Hello PDF")
			eq(t, a.Size, 9)
			eq(t, a.Index, 0)
		}},
		{"latin1.eml", func(t *testing.T, p *Parsed) {
			eq(t, p.Subject, "Café crème")
			eq(t, p.From.Name, "André")
			eq(t, strings.TrimSpace(p.Text), "Café au lait")
		}},
		{"b64subject.eml", func(t *testing.T, p *Parsed) {
			eq(t, p.Subject, "こんにちは")
			eq(t, strings.TrimSpace(p.Text), "こんにちは")
		}},
		{"no_message_id.eml", func(t *testing.T, p *Parsed) {
			if !strings.HasPrefix(p.MessageID, "sha256:") || len(p.MessageID) != 7+64 {
				t.Errorf("id = %q", p.MessageID)
			}
			eq(t, p.HasRealID, false)
		}},
		{"reply.eml", func(t *testing.T, p *Parsed) {
			eq(t, p.InReplyTo, "plain-1@example.com")
			eq(t, strings.Join(p.References, ","), "root-0@example.com,plain-1@example.com")
		}},
		{"malformed.eml", func(t *testing.T, p *Parsed) {
			eq(t, p.MessageID, "bad-1@example.org")
			if !strings.Contains(p.Text, "Recoverable body") {
				t.Errorf("text = %q", p.Text)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) { tc.check(t, load(t, tc.file)) })
	}
}

func TestMessageIDFallbackStable(t *testing.T) {
	a, b := load(t, "no_message_id.eml"), load(t, "no_message_id.eml")
	eq(t, a.MessageID, b.MessageID)
}

func TestSnippetTruncates(t *testing.T) {
	s := snippet(strings.Repeat("word  \n", 100))
	eq(t, len([]rune(s)) <= SnippetLen, true)
	if strings.Contains(s, "  ") || strings.Contains(s, "\n") {
		t.Errorf("whitespace not collapsed: %q", s)
	}
}

func TestSplitAddress(t *testing.T) {
	tests := []struct{ in, local, tag, domain string }{
		{"shop@x.com", "shop", "", "x.com"},
		{"Shop+Orders@X.com", "shop", "orders", "x.com"},
		{"a+b+c@x.com", "a", "b+c", "x.com"},
		{"nodomain", "nodomain", "", ""},
	}
	for _, tc := range tests {
		l, tg, d := SplitAddress(tc.in)
		if l != tc.local || tg != tc.tag || d != tc.domain {
			t.Errorf("%s: got %q %q %q", tc.in, l, tg, d)
		}
	}
}

func TestNormalizeSubject(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Hello There", "hello there"},
		{"Re: RE: Hello  There", "hello there"},
		{"Fwd: Re: Hello There", "hello there"},
		{"FW: x", "x"},
		{"Re[2]: x", "x"},
		{"Regarding x", "regarding x"},
	}
	for _, tc := range tests {
		eq(t, NormalizeSubject(tc.in), tc.want)
	}
}

func TestThreadKey(t *testing.T) {
	root := load(t, "plain.eml")
	reply := load(t, "reply.eml")
	subjOnly := ThreadKey(root, nil)
	eq(t, strings.HasPrefix(subjOnly, "subj:"), true)
	// reply without lookup hit falls back to the same normalised subject
	eq(t, ThreadKey(reply, func([]string) (string, bool) { return "", false }), subjOnly)
	// lookup hit wins, and ids are ordered In-Reply-To first then References newest-first
	var got []string
	k := ThreadKey(reply, func(ids []string) (string, bool) { got = ids; return "thread:42", true })
	eq(t, k, "thread:42")
	eq(t, strings.Join(got, ","), "plain-1@example.com,root-0@example.com")
}

func eq[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}
