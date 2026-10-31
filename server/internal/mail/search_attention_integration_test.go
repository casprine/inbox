package mail_test

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/events"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/store"
)

func sp(s string) *string { return &s }

type seedMsg struct {
	biz       *uuid.UUID
	from, sub string
	body      string
	to        string
	atts      int
	archived  bool
	route     string
	age       time.Duration
}

func (e *env) business(t *testing.T, name, domain string, order int) uuid.UUID {
	t.Helper()
	b, err := e.st.CreateBusiness(context.Background(), store.Business{ID: ids.NewV7(), Name: name, Domain: domain, CFZoneID: "z", SortOrder: order, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return b.ID
}

func (e *env) seed(t *testing.T, s seedMsg) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	if s.route == "" {
		s.route = "routed"
	}
	id := ids.NewV7()
	m := store.Message{ID: id, BusinessID: s.biz, Source: "edge", MessageIDHdr: "<" + id.String() + "@t>", EnvTo: sp(s.to),
		EnvToAll: []string{s.to}, FromAddr: sp(s.from), FromName: sp(strings.Split(s.from, "@")[0]), Subject: sp(s.sub), Snippet: sp(s.body),
		ThreadKey: id.String(), ReceivedAt: time.Now().UTC().Add(-s.age), AttachmentCount: s.atts, Route: s.route}
	if s.archived {
		now := time.Now()
		m.ArchivedAt = &now
	}
	if ok, err := e.st.InsertMessage(ctx, m); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if s.archived { // InsertMessage does not set archived_at
		if _, err := e.st.SetMessagesArchived(ctx, []uuid.UUID{id}, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.st.UpsertMessageSearch(ctx, id, s.sub, s.from, s.body, ""); err != nil {
		t.Fatal(err)
	}
	return id
}

type searchResp struct {
	Total  int `json:"total"`
	Groups []struct {
		Business *struct{ Name string } `json:"business"`
		Total    int                    `json:"total"`
		Messages []struct {
			ID         string `json:"id"`
			Archived   bool   `json:"archived"`
			Highlights struct {
				Subject []struct{ Start, End int } `json:"subject"`
			} `json:"highlights"`
		} `json:"messages"`
	} `json:"groups"`
	Parsed struct {
		Prefix string   `json:"prefix"`
		Terms  []string `json:"terms"`
	} `json:"parsed"`
}

func (e *env) search(t *testing.T, q string, biz string) searchResp {
	t.Helper()
	path := "/search?q=" + url.QueryEscape(q)
	if biz != "" {
		path += "&business=" + biz
	}
	st, body, _ := e.do(t, "GET", path, nil)
	if st != 200 {
		t.Fatalf("search %q: %d %s", q, st, body)
	}
	var r searchResp
	decodeInto(t, body, &r)
	return r
}

func TestSearch(t *testing.T) {
	e := setup(t)
	a := e.business(t, "Northgate", "northgate.com", 0)
	b := e.business(t, "Acme", "acme.io", 1)
	m1 := e.seed(t, seedMsg{biz: &a, from: "billing@vendor.com", sub: "Invoice 1042 for March", body: "please pay the quarterly report fee", to: "orders@northgate.com", atts: 1, age: time.Hour})
	m2 := e.seed(t, seedMsg{biz: &a, from: "alice@example.org", sub: "Lunch", body: "northgate offsite plans", to: "hello@northgate.com", archived: true, age: 2 * time.Hour})
	m3 := e.seed(t, seedMsg{biz: &b, from: "bob@vendor.com", sub: "Invoice question", body: "unrelated", to: "hello@acme.io", age: 3 * time.Hour})
	m4 := e.seed(t, seedMsg{from: "carol@elsewhere.net", sub: "Northgate stray", body: "stray", to: "x@y.z", route: "unrouted", age: 4 * time.Hour})
	_ = m3

	ids := func(r searchResp) []string {
		var out []string
		for _, g := range r.Groups {
			for _, m := range g.Messages {
				out = append(out, m.ID)
			}
		}
		return out
	}
	// bare word, grouped by business order, unassigned last, archived included and flagged
	r := e.search(t, "northgate", "")
	if r.Total != 2 || len(r.Groups) != 2 || r.Groups[0].Business == nil || r.Groups[0].Business.Name != "Northgate" || r.Groups[1].Business != nil {
		t.Fatalf("%+v", r)
	}
	if r.Groups[0].Messages[0].ID != m2.String() || !r.Groups[0].Messages[0].Archived || r.Groups[1].Messages[0].ID != m4.String() {
		t.Fatalf("%+v", r)
	}
	// m2: body matches (snippet); m4 subject highlight offsets 0..9
	if h := r.Groups[1].Messages[0].Highlights.Subject; len(h) != 1 || h[0].Start != 0 || h[0].End != 9 {
		t.Fatalf("highlight %+v", h)
	}
	// prefix on the last token
	if r := e.search(t, "northg", ""); r.Total != 2 || r.Parsed.Prefix != "northg" {
		t.Fatalf("%+v", r)
	}
	// ... but a trailing space makes it exact
	if r := e.search(t, "northg ", ""); r.Total != 0 {
		t.Fatalf("%+v", r)
	}
	// scoped to a business
	if r := e.search(t, "invoice", b.String()); r.Total != 1 || r.Groups[0].Business.Name != "Acme" {
		t.Fatalf("%+v", r)
	}
	// operators
	if got := ids(e.search(t, "from:billing", "")); len(got) != 1 || got[0] != m1.String() {
		t.Fatal(got)
	}
	if got := ids(e.search(t, "from:vendor.com", "")); len(got) != 2 {
		t.Fatal(got)
	}
	if got := ids(e.search(t, "to:hello@northgate.com", "")); len(got) != 1 || got[0] != m2.String() {
		t.Fatal(got)
	}
	if got := ids(e.search(t, "has:attachment", "")); len(got) != 1 || got[0] != m1.String() {
		t.Fatal(got)
	}
	if got := ids(e.search(t, "invoice has:attachment from:vendor", "")); len(got) != 1 || got[0] != m1.String() {
		t.Fatal(got)
	}
	// phrases
	if got := ids(e.search(t, `"quarterly report"`, "")); len(got) != 1 || got[0] != m1.String() {
		t.Fatal(got)
	}
	if got := ids(e.search(t, `"report quarterly"`, "")); len(got) != 0 {
		t.Fatal(got)
	}
	// LIKE wildcards in operator values are literal
	if got := ids(e.search(t, "from:%", "")); len(got) != 0 {
		t.Fatal(got)
	}
	// hostile input does not break SQL / tsquery
	for _, q := range []string{"'; DROP TABLE messages;--", "a & | ! ( ) :* <->", `"""`, "to:'", "-x or y", "\\"} {
		if st, body, _ := e.do(t, "GET", "/search?q="+url.QueryEscape(q), nil); st >= 500 {
			t.Fatalf("%q: %d %s", q, st, body)
		}
	}
	// errors
	for _, p := range []string{"/search", "/search?q=", "/search?q=from%3A", "/search?q=" + strings.Repeat("a", 201), "/search?q=x&business=nope"} {
		if st, body, _ := e.do(t, "GET", p, nil); st != 400 {
			t.Fatalf("%s: %d %s", p, st, body)
		}
	}
	// punctuation-only free text alongside nothing else is an empty query
	if st, _, _ := e.do(t, "GET", "/search?q=%21%21%21", nil); st != 400 {
		t.Fatal(st)
	}
}

func TestSearchRecents(t *testing.T) {
	e := setup(t)
	get := func() []string {
		st, body, _ := e.do(t, "GET", "/search/recents", nil)
		if st != 200 {
			t.Fatal(st, string(body))
		}
		var r struct{ Recents []string }
		decodeInto(t, body, &r)
		return r.Recents
	}
	if got := get(); got == nil || len(got) != 0 {
		t.Fatalf("%v", got)
	}
	for i := 0; i < 12; i++ {
		if st, body, _ := e.do(t, "POST", "/search/recents", map[string]string{"q": "query " + string(rune('a'+i))}); st != 200 {
			t.Fatal(st, string(body))
		}
	}
	e.do(t, "POST", "/search/recents", map[string]string{"q": "QUERY D"})
	got := get()
	if len(got) != 10 || got[0] != "QUERY D" || got[1] != "query l" {
		t.Fatalf("%v", got)
	}
	for _, g := range got[1:] {
		if strings.EqualFold(g, "query d") {
			t.Fatalf("dup %v", got)
		}
	}
	if st, _, _ := e.do(t, "POST", "/search/recents", map[string]string{"q": "  "}); st != 400 {
		t.Fatal(st)
	}
	if st, _, _ := e.do(t, "DELETE", "/search/recents?q="+url.QueryEscape("query l"), nil); st != 204 || len(get()) != 9 {
		t.Fatal(st, get())
	}
	if st, _, _ := e.do(t, "DELETE", "/search/recents", nil); st != 204 || len(get()) != 0 {
		t.Fatal(st, get())
	}
}

func TestAttention(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := e.business(t, "Northgate", "northgate.com", 0)
	b := e.business(t, "Acme", "acme.io", 1)
	u1 := e.seed(t, seedMsg{biz: &a, from: "x@a.com", sub: "u1", to: "x@northgate.com", route: "unrouted"})
	e.seed(t, seedMsg{biz: &a, from: "x@a.com", sub: "u2", to: "x@northgate.com", route: "unrouted", age: time.Minute})
	e.seed(t, seedMsg{biz: &b, from: "x@a.com", sub: "u3", to: "x@acme.io", route: "unrouted"})
	e.seed(t, seedMsg{from: "x@a.com", sub: "u4", to: "x@nowhere.io", route: "unrouted"})
	e.seed(t, seedMsg{biz: &a, from: "x@a.com", sub: "routed", to: "x@northgate.com"})
	e.seed(t, seedMsg{biz: &a, from: "x@a.com", sub: "archived unrouted", to: "x@northgate.com", route: "unrouted", archived: true})

	mkFailed := func(key *string, hdr string) uuid.UUID {
		id := ids.NewV7()
		m := store.Message{ID: id, Source: "edge", State: "failed", FailureReason: sp("parse_failed"), MessageIDHdr: hdr, ThreadKey: hdr,
			EnvTo: sp("x@northgate.com"), FromAddr: sp("f@a.com"), RawKey: key, Route: "unrouted", ReceivedAt: time.Now().UTC()}
		if ok, err := e.st.InsertMessage(ctx, m); err != nil || !ok {
			t.Fatal(ok, err)
		}
		return id
	}
	withRaw := mkFailed(sp("raw/2026/x1"), "raw:x1")
	noRaw := mkFailed(nil, "failed:abc")
	dismissMe := mkFailed(sp("raw/2026/x2"), "raw:x2")

	st, body, _ := e.do(t, "GET", "/attention", nil)
	if st != 200 {
		t.Fatal(st, string(body))
	}
	var at struct {
		Unrouted struct {
			Count int
			By    []struct {
				Name  *string
				Count int
			} `json:"by_business"`
		}
		Failed struct{ Count int }
		Total  int
	}
	decodeInto(t, body, &at)
	if at.Unrouted.Count != 4 || at.Failed.Count != 3 || at.Total != 7 || len(at.Unrouted.By) != 3 || at.Unrouted.By[0].Count != 2 || *at.Unrouted.By[0].Name != "Northgate" {
		t.Fatalf("%s", body)
	}

	st, body, _ = e.do(t, "GET", "/attention/unrouted", nil)
	var un struct {
		Total  int
		Groups []struct {
			Business *struct{ Name string }
			Count    int
			Messages []struct{ ID string }
		}
	}
	decodeInto(t, body, &un)
	if st != 200 || un.Total != 4 || len(un.Groups) != 3 || un.Groups[0].Business.Name != "Northgate" || un.Groups[0].Count != 2 ||
		un.Groups[0].Messages[0].ID != u1.String() || un.Groups[2].Business != nil {
		t.Fatalf("%d %s", st, body)
	}

	st, body, _ = e.do(t, "GET", "/attention/failed", nil)
	var fl struct {
		Messages []struct {
			ID     string
			HasRaw bool   `json:"has_raw"`
			Reason string `json:"failure_reason"`
		}
	}
	decodeInto(t, body, &fl)
	if st != 200 || len(fl.Messages) != 3 || fl.Messages[0].Reason != "parse_failed" {
		t.Fatalf("%d %s", st, body)
	}

	// retry without a stored copy -> 409 needs_gmail, row untouched
	if st, body, _ := e.do(t, "POST", "/failed/"+noRaw.String()+":retry", nil); st != 409 || !strings.Contains(string(body), "needs_gmail") {
		t.Fatal(st, string(body))
	}
	// retry with raw: enqueued, failed row removed (the job recreates it)
	if st, body, _ := e.do(t, "POST", "/failed/"+withRaw.String()+":retry", nil); st != 202 || len(e.retry.keys) != 1 || e.retry.keys[0] != "raw/2026/x1" {
		t.Fatal(st, string(body), e.retry.keys)
	}
	if _, err := e.st.GetMessage(ctx, withRaw); err != store.ErrNotFound {
		t.Fatalf("failed row should be gone: %v", err)
	}
	if st, _, _ := e.do(t, "POST", "/failed/"+withRaw.String()+":retry", nil); st != 404 {
		t.Fatal(st)
	}
	// enqueue failure restores the row
	e.retry.err = context.DeadlineExceeded
	if st, body, _ := e.do(t, "POST", "/failed/"+dismissMe.String()+":retry", nil); st != 503 || !strings.Contains(string(body), "enqueue_failed") {
		t.Fatal(st, string(body))
	}
	if _, err := e.st.GetMessage(ctx, dismissMe); err != nil {
		t.Fatalf("row not restored: %v", err)
	}
	// dismiss
	if st, _, _ := e.do(t, "POST", "/failed/"+dismissMe.String()+":dismiss", nil); st != 204 {
		t.Fatal(st)
	}
	if st, _, _ := e.do(t, "POST", "/failed/"+dismissMe.String()+":dismiss", nil); st != 404 {
		t.Fatal(st)
	}
	if st, _, _ := e.do(t, "POST", "/failed/"+uuid.NewString()+":dismiss", nil); st != 404 {
		t.Fatal(st)
	}
	// a stored message is not a failed one
	if st, _, _ := e.do(t, "POST", "/failed/"+u1.String()+":retry", nil); st != 404 {
		t.Fatal(st)
	}
	_, body, _ = e.do(t, "GET", "/attention", nil)
	decodeInto(t, body, &at)
	if at.Failed.Count != 1 {
		t.Fatalf("%s", body)
	}
}

func TestEventsSSE(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	req, _ := http.NewRequest("GET", e.srv.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Cache-Control") != "no-store" ||
		resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	lines := make(chan string, 100)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	waitFor := func(substr string) string {
		t.Helper()
		timeout := time.After(5 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("stream ended waiting for %q", substr)
				}
				if strings.Contains(l, substr) {
					return l
				}
			case <-timeout:
				t.Fatalf("timeout waiting for %q", substr)
			}
		}
	}
	waitFor(": connected")
	waitFor(": ping") // heartbeat (150ms in tests)

	// ingest-style NOTIFY with a real message
	biz := e.business(t, "Northgate", "northgate.com", 0)
	id := e.seed(t, seedMsg{biz: &biz, from: "x@a.com", sub: "hi", to: "x@northgate.com"})
	if err := e.st.NotifyMailNew(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitFor("event: mail.new")
	d := waitFor("data: ")
	if !strings.Contains(d, `"type":"mail.new"`) || !strings.Contains(d, id.String()) || !strings.Contains(d, biz.String()) {
		t.Fatal(d)
	}
	// Publish API
	e.hub.Publish(eventsNew("gmail.status", map[string]any{"status": "needs_reauth"}))
	waitFor("event: gmail.status")
	if d := waitFor("data: "); !strings.Contains(d, `"status":"needs_reauth"`) || !strings.Contains(d, `"type":"gmail.status"`) {
		t.Fatal(d)
	}
	// auth is required
	if r, _ := http.Get(e.srv.URL + "/v1/events"); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	// shutdown ends streams
	e.hub.Close()
	for range lines {
	}
	if e.hub.Subscribers() != 0 {
		t.Fatal("subscribers left")
	}
}

func eventsNew(typ string, f map[string]any) events.Event { return events.New(typ, f) }

func TestSearchPagination(t *testing.T) {
	e := setup(t)
	biz1 := e.business(t, "Acme", "acme.test", 0)
	biz2 := e.business(t, "Beta", "beta.test", 1)
	// 7 matches across two businesses, interleaved in time; plus noise that must never appear.
	want := []uuid.UUID{}
	for i := 0; i < 7; i++ {
		b := &biz1
		if i%2 == 1 {
			b = &biz2
		}
		want = append(want, e.seed(t, seedMsg{biz: b, from: "pat@client.com", sub: fmt.Sprintf("Quarterly report %d", i), body: "numbers", to: "a@acme.test", age: time.Duration(i+1) * time.Hour}))
	}
	e.seed(t, seedMsg{biz: &biz1, from: "x@y.com", sub: "Unrelated", body: "nothing", to: "a@acme.test"})

	type page struct {
		searchResp
		HasMore    bool    `json:"has_more"`
		NextCursor *string `json:"next_cursor"`
	}
	get := func(path string) (int, page) {
		st, body, _ := e.do(t, "GET", path, nil)
		var p page
		if st == 200 {
			decodeInto(t, body, &p.searchResp)
			decodeInto(t, body, &p)
		}
		return st, p
	}
	ids := func(p page) []string {
		var out []string
		for _, g := range p.Groups {
			for _, m := range g.Messages {
				out = append(out, m.ID)
			}
		}
		return out
	}

	var seen []string
	cursor := ""
	for i, wantN := range []int{3, 3, 1} {
		path := "/search?q=quarterly&limit=3"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		st, p := get(path)
		if st != 200 || p.Total != wantN || len(ids(p)) != wantN {
			t.Fatalf("page %d: status %d total %d ids %d, want %d", i, st, p.Total, len(ids(p)), wantN)
		}
		if last := i == 2; p.HasMore == last || (p.NextCursor == nil) != last {
			t.Fatalf("page %d: has_more=%v next_cursor=%v", i, p.HasMore, p.NextCursor)
		}
		seen = append(seen, ids(p)...)
		if p.NextCursor != nil {
			cursor = *p.NextCursor
		}
	}
	// Every match exactly once, newest first (age grows with i), nothing else.
	if len(seen) != 7 {
		t.Fatalf("seen %d", len(seen))
	}
	uniq := map[string]bool{}
	for i, id := range seen {
		uniq[id] = true
		_ = i
	}
	if len(uniq) != 7 {
		t.Fatal("duplicate results across pages")
	}
	order := make([]string, 0, 7)
	// Within a page results are grouped by business, so check the global order via the cursor positions instead:
	// the page boundaries must respect time: every id on page 1 is newer than every id on page 2.
	_, p1 := get("/search?q=quarterly&limit=3")
	_, p2 := get("/search?q=quarterly&limit=3&cursor=" + url.QueryEscape(*p1.NextCursor))
	newest := map[string]int{}
	for i, id := range want {
		newest[id.String()] = i
	}
	for _, a := range ids(p1) {
		for _, b := range ids(p2) {
			if newest[a] > newest[b] {
				t.Fatalf("page 1 result %d is older than page 2 result %d", newest[a], newest[b])
			}
		}
	}
	_ = order

	// Default limit is unchanged (100, so everything here fits and there is no next page).
	if st, p := get("/search?q=quarterly"); st != 200 || p.Total != 7 || p.HasMore || p.NextCursor != nil {
		t.Fatalf("default page: %d %+v", st, p)
	}
	// Business filter composes with paging.
	if st, p := get("/search?q=quarterly&limit=2&business=" + biz2.String()); st != 200 || p.Total != 2 || !p.HasMore {
		t.Fatalf("business filter: %d total=%d more=%v", st, p.Total, p.HasMore)
	}
	// Bad parameters.
	for _, path := range []string{"/search?q=a&limit=0", "/search?q=a&limit=201", "/search?q=a&limit=x"} {
		if st, _, _ := e.do(t, "GET", path, nil); st != 400 {
			t.Errorf("%s: %d", path, st)
		}
	}
	if st, body, _ := e.do(t, "GET", "/search?q=quarterly&cursor=garbage", nil); st != 400 || !strings.Contains(string(body), "invalid_cursor") {
		t.Errorf("bad cursor: %d %s", st, body)
	}
	// A cursor past the last result gives an empty final page.
	if st, p := get("/search?q=quarterly&limit=7&cursor=" + url.QueryEscape(store.EncodeCursor(store.Cursor{ReceivedAt: time.Now().Add(-100 * time.Hour), ID: ids0()}))); st != 200 || p.Total != 0 || p.HasMore {
		t.Fatalf("past the end: %d %+v", st, p)
	}
}

func ids0() uuid.UUID { return uuid.UUID{} }
