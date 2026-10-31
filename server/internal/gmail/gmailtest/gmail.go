package gmailtest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Msg is a message in the fake mailbox.
type Msg struct {
	ID        string
	ThreadID  string
	MessageID string // RFC 5322 Message-ID without angle brackets
	Labels    map[string]bool
	Raw       []byte
	Date      time.Time
	To        []string // lower-cased recipients, for q=to:/deliveredto:
}

// Filter is a Gmail filter as returned by settings.filters.list.
type Filter struct {
	ID       string
	From     string
	To       string
	Subject  string
	AddLabel string // label name
	Archive  bool
	MarkRead bool
	Delete   bool
	Forward  string
}

type gmailData struct {
	msgs    map[string]*Msg
	order   []string
	labels  map[string]string // id -> name
	filters []Filter
	seq     int
}

func (g *gmailData) init() {
	g.msgs = map[string]*Msg{}
	g.labels = map[string]string{}
	for _, l := range []string{"INBOX", "UNREAD", "SENT", "TRASH", "SPAM", "STARRED", "IMPORTANT", "CATEGORY_PROMOTIONS"} {
		g.labels[l] = l
	}
}

// AddMessage puts a message in the mailbox (labels default to INBOX+UNREAD).
func (f *Fake) AddMessage(m Msg) *Msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := &f.gm
	g.seq++
	if m.ID == "" {
		m.ID = fmt.Sprintf("gm%04d", g.seq)
	}
	if m.ThreadID == "" {
		m.ThreadID = "th-" + m.ID
	}
	if m.Labels == nil {
		m.Labels = map[string]bool{"INBOX": true, "UNREAD": true}
	}
	if m.Date.IsZero() {
		m.Date = time.Now()
	}
	cp := m
	g.msgs[m.ID] = &cp
	g.order = append(g.order, m.ID)
	return &cp
}

// AddFilter registers a filter.
func (f *Fake) AddFilter(fl Filter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gm.seq++
	if fl.ID == "" {
		fl.ID = fmt.Sprintf("flt%d", f.gm.seq)
	}
	f.gm.filters = append(f.gm.filters, fl)
}

// AddLabel creates a user label and returns its id.
func (f *Fake) AddLabel(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addLabelLocked(name)
}

func (f *Fake) addLabelLocked(name string) string {
	g := &f.gm
	g.seq++
	id := fmt.Sprintf("Label_%d", g.seq)
	g.labels[id] = name
	return id
}

// DeleteLabel removes a label (as when the user deletes it in Gmail).
func (f *Fake) DeleteLabel(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, n := range f.gm.labels {
		if n == name {
			delete(f.gm.labels, id)
		}
	}
}

// Message returns a snapshot of a message, or nil.
func (f *Fake) Message(id string) *Msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.gm.msgs[id]
	if m == nil {
		return nil
	}
	cp := *m
	cp.Labels = map[string]bool{}
	for k, v := range m.Labels {
		cp.Labels[k] = v
	}
	return &cp
}

// LabelNames returns the names of a message's labels (system labels by id) sorted.
func (f *Fake) LabelNames(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.gm.msgs[id]
	if m == nil {
		return nil
	}
	var out []string
	for l := range m.Labels {
		if n, ok := f.gm.labels[l]; ok {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// LabelID returns the id of a label name ("" if absent).
func (f *Fake) LabelID(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, n := range f.gm.labels {
		if n == name {
			return id
		}
	}
	return ""
}

func (f *Fake) gmail(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me")
	f.mu.Lock()
	defer f.mu.Unlock()
	g := &f.gm
	switch {
	case p == "/profile" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"emailAddress": f.Email, "messagesTotal": len(g.msgs)})

	case p == "/messages" && r.Method == http.MethodGet:
		f.listMessages(w, r)

	case strings.HasPrefix(p, "/messages/") && strings.HasSuffix(p, "/modify") && r.Method == http.MethodPost:
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/messages/"), "/modify")
		m := g.msgs[id]
		if m == nil {
			gErr(w, http.StatusNotFound, "notFound", "Requested entity was not found.")
			return
		}
		var in struct {
			Add    []string `json:"addLabelIds"`
			Remove []string `json:"removeLabelIds"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		for _, l := range append(append([]string{}, in.Add...), in.Remove...) {
			if _, ok := g.labels[l]; !ok {
				gErr(w, http.StatusBadRequest, "invalidArgument", "Invalid label: "+l)
				return
			}
		}
		for _, l := range in.Add {
			m.Labels[l] = true
		}
		for _, l := range in.Remove {
			delete(m.Labels, l)
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": m.ID, "threadId": m.ThreadID, "labelIds": labelIDs(m)})

	case strings.HasPrefix(p, "/messages/") && r.Method == http.MethodGet:
		m := g.msgs[strings.TrimPrefix(p, "/messages/")]
		if m == nil {
			gErr(w, http.StatusNotFound, "notFound", "Requested entity was not found.")
			return
		}
		out := map[string]any{"id": m.ID, "threadId": m.ThreadID, "labelIds": labelIDs(m), "internalDate": strconv.FormatInt(m.Date.UnixMilli(), 10)}
		if r.URL.Query().Get("format") == "raw" {
			out["raw"] = base64.RawURLEncoding.EncodeToString(m.Raw)
		}
		writeJSON(w, http.StatusOK, out)

	case strings.HasPrefix(p, "/threads/") && r.Method == http.MethodGet:
		tid := strings.TrimPrefix(p, "/threads/")
		var msgs []map[string]any
		for _, id := range g.order {
			if m := g.msgs[id]; m.ThreadID == tid {
				msgs = append(msgs, map[string]any{"id": m.ID, "threadId": m.ThreadID, "labelIds": labelIDs(m)})
			}
		}
		if msgs == nil {
			gErr(w, http.StatusNotFound, "notFound", "Requested entity was not found.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": tid, "messages": msgs})

	case p == "/labels" && r.Method == http.MethodGet:
		type lab struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		}
		var out []lab
		for id, n := range g.labels {
			t := "user"
			if id == n {
				t = "system"
			}
			out = append(out, lab{id, n, t})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		writeJSON(w, http.StatusOK, map[string]any{"labels": out})

	case p == "/labels" && r.Method == http.MethodPost:
		var in struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		for _, n := range g.labels {
			if strings.EqualFold(n, in.Name) {
				gErr(w, http.StatusConflict, "duplicate", "Label name exists or conflicts with another label name.")
				return
			}
		}
		id := f.addLabelLocked(in.Name)
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": in.Name, "type": "user"})

	case p == "/settings/filters" && r.Method == http.MethodGet:
		out := []map[string]any{}
		for _, fl := range g.filters {
			crit := map[string]any{}
			if fl.From != "" {
				crit["from"] = fl.From
			}
			if fl.To != "" {
				crit["to"] = fl.To
			}
			if fl.Subject != "" {
				crit["subject"] = fl.Subject
			}
			act := map[string]any{}
			var add, rm []string
			if fl.AddLabel != "" {
				for id, n := range g.labels {
					if n == fl.AddLabel {
						add = append(add, id)
					}
				}
			}
			if fl.Archive {
				rm = append(rm, "INBOX")
			}
			if fl.MarkRead {
				rm = append(rm, "UNREAD")
			}
			if fl.Delete {
				add = append(add, "TRASH")
			}
			if add != nil {
				act["addLabelIds"] = add
			}
			if rm != nil {
				act["removeLabelIds"] = rm
			}
			if fl.Forward != "" {
				act["forward"] = fl.Forward
			}
			out = append(out, map[string]any{"id": fl.ID, "criteria": crit, "action": act})
		}
		writeJSON(w, http.StatusOK, map[string]any{"filter": out})

	default:
		gErr(w, http.StatusNotFound, "notFound", "not found")
	}
}

func labelIDs(m *Msg) []string {
	var out []string
	for l := range m.Labels {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// listMessages supports q terms: rfc822msgid:, to:, deliveredto:, in:sent, after:YYYY/MM/DD, before:; plus
// maxResults / pageToken (numeric offset). Caller holds f.mu.
func (f *Fake) listMessages(w http.ResponseWriter, r *http.Request) {
	g := &f.gm
	q := r.URL.Query()
	var match []*Msg
	for _, id := range g.order {
		if matches(g.msgs[id], q.Get("q"), q.Get("includeSpamTrash") == "true") {
			match = append(match, g.msgs[id])
		}
	}
	// newest first, like Gmail
	sort.SliceStable(match, func(i, j int) bool { return match[i].Date.After(match[j].Date) })
	max := 100
	if v, err := strconv.Atoi(q.Get("maxResults")); err == nil && v > 0 {
		max = v
	}
	start := 0
	if t := q.Get("pageToken"); t != "" {
		start, _ = strconv.Atoi(t)
	}
	end := start + max
	if end > len(match) {
		end = len(match)
	}
	if start > len(match) {
		start = len(match)
	}
	out := map[string]any{"resultSizeEstimate": len(match)}
	var items []map[string]string
	for _, m := range match[start:end] {
		items = append(items, map[string]string{"id": m.ID, "threadId": m.ThreadID})
	}
	if items != nil {
		out["messages"] = items
	}
	if end < len(match) {
		out["nextPageToken"] = strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, out)
}

func matches(m *Msg, q string, includeSpamTrash bool) bool {
	if !includeSpamTrash && (m.Labels["TRASH"] || m.Labels["SPAM"]) {
		return false
	}
	for _, term := range strings.Fields(q) {
		neg := strings.HasPrefix(term, "-")
		k, v, ok := strings.Cut(strings.TrimPrefix(term, "-"), ":")
		if !ok {
			continue
		}
		v = strings.Trim(v, "<>")
		hit := true
		switch k {
		case "rfc822msgid":
			hit = m.MessageID == v
		case "in":
			switch v {
			case "sent":
				hit = m.Labels["SENT"]
			case "drafts":
				hit = m.Labels["DRAFT"]
			}
		case "to", "deliveredto":
			hit = false
			for _, t := range m.To {
				if strings.HasPrefix(v, "@") && strings.HasSuffix(t, v) || t == strings.ToLower(v) {
					hit = true
				}
			}
		case "after":
			if t, err := time.Parse("2006/01/02", v); err == nil {
				hit = m.Date.After(t)
			}
		case "before":
			if t, err := time.Parse("2006/01/02", v); err == nil {
				hit = m.Date.Before(t)
			}
		}
		if hit == neg {
			return false
		}
	}
	return true
}
