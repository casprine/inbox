// Package cftest is a fake Cloudflare API for tests and local end-to-end runs.
package cftest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

// Zone is a fake zone with its Email Routing setup.
type Zone struct {
	ID, Name, Status string
	RoutingEnabled   bool
	// CatchAll: type "worker" (Value is the worker name), "forward", "drop"; "" = no catch-all rule.
	CatchAllType  string
	CatchAllValue string
	CatchAllOn    bool
}

// Fake is the server. Requests logs "METHOD path?query".
type Fake struct {
	*httptest.Server
	mu       sync.Mutex
	zones    []*Zone
	Requests []string
	// Token that is accepted; other tokens get 403.
	Token string
	// PerPage lets tests force pagination.
	PerPage int
	fail    map[string]int // path prefix -> status, consumed once
}

func New(token string) *Fake {
	f := &Fake{Token: token, PerPage: 50, fail: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func (f *Fake) AddZone(z Zone) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := z
	if cp.Status == "" {
		cp.Status = "active"
	}
	f.zones = append(f.zones, &cp)
}

// Set changes a zone's routing setup by id.
func (f *Fake) Set(id string, fn func(*Zone)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, z := range f.zones {
		if z.ID == id {
			fn(z)
		}
	}
}

// FailNext makes the next request under pathPrefix answer with status.
func (f *Fake) FailNext(pathPrefix string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[pathPrefix] = status
}

// Count of logged requests whose line starts with prefix.
func (f *Fake) Count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.Requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func write(w http.ResponseWriter, code int, result any, info map[string]int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	body := map[string]any{"success": code/100 == 2, "errors": []any{}, "messages": []any{}, "result": result}
	if code/100 != 2 {
		body["errors"] = []map[string]any{{"code": code, "message": http.StatusText(code)}}
	}
	if info != nil {
		body["result_info"] = info
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Requests = append(f.Requests, r.Method+" "+r.URL.RequestURI())
	if r.Header.Get("Authorization") != "Bearer "+f.Token {
		write(w, http.StatusForbidden, nil, nil)
		return
	}
	for p, st := range f.fail {
		if strings.HasPrefix(r.URL.Path, p) {
			delete(f.fail, p)
			write(w, st, nil, nil)
			return
		}
	}
	p := r.URL.Path
	switch {
	case p == "/zones":
		var match []*Zone
		for _, z := range f.zones {
			if n := r.URL.Query().Get("name"); n == "" || strings.EqualFold(n, z.Name) {
				match = append(match, z)
			}
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		per := f.PerPage
		total := (len(match) + per - 1) / per
		if total == 0 {
			total = 1
		}
		lo, hi := (page-1)*per, page*per
		if lo > len(match) {
			lo = len(match)
		}
		if hi > len(match) {
			hi = len(match)
		}
		out := []map[string]string{}
		for _, z := range match[lo:hi] {
			out = append(out, map[string]string{"id": z.ID, "name": z.Name, "status": z.Status})
		}
		write(w, 200, out, map[string]int{"page": page, "total_pages": total})
	case strings.HasPrefix(p, "/zones/") && strings.HasSuffix(p, "/email/routing/rules/catch_all"):
		z := f.find(strings.TrimSuffix(strings.TrimPrefix(p, "/zones/"), "/email/routing/rules/catch_all"))
		if z == nil || z.CatchAllType == "" {
			write(w, http.StatusNotFound, nil, nil)
			return
		}
		act := map[string]any{"type": z.CatchAllType}
		if z.CatchAllType != "drop" {
			act["value"] = []string{z.CatchAllValue}
		}
		write(w, 200, map[string]any{"enabled": z.CatchAllOn, "name": "Catch-all", "matchers": []map[string]string{{"type": "all"}}, "actions": []any{act}}, nil)
	case strings.HasPrefix(p, "/zones/") && strings.HasSuffix(p, "/email/routing"):
		z := f.find(strings.TrimSuffix(strings.TrimPrefix(p, "/zones/"), "/email/routing"))
		if z == nil {
			write(w, http.StatusNotFound, nil, nil)
			return
		}
		write(w, 200, map[string]any{"enabled": z.RoutingEnabled, "name": z.Name}, nil)
	default:
		write(w, http.StatusNotFound, nil, nil)
	}
}

func (f *Fake) find(id string) *Zone {
	for _, z := range f.zones {
		if z.ID == id {
			return z
		}
	}
	return nil
}
