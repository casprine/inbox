package cloudflare_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/auth"
	"github.com/casprine/inbox/server/internal/cloudflare"
	"github.com/casprine/inbox/server/internal/cloudflare/cftest"
	"github.com/casprine/inbox/server/internal/events"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/mail"
	"github.com/casprine/inbox/server/internal/store"
	"github.com/casprine/inbox/server/internal/testutil"
)

type pub struct {
	mu sync.Mutex
	ev []events.Event
}

func (p *pub) Publish(e events.Event) { p.mu.Lock(); p.ev = append(p.ev, e); p.mu.Unlock() }
func (p *pub) count(typ string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.ev {
		if e.Type == typ {
			n++
		}
	}
	return n
}

type env struct {
	db   *testutil.DB
	fake *cftest.Fake
	svc  *cloudflare.Service
	srv  *httptest.Server
	ev   *pub
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testutil.NewDB(t)
	fake := cftest.New("cf-token")
	t.Cleanup(fake.Close)
	ev := &pub{}
	svc := &cloudflare.Service{Store: db.Store, Events: ev, Client: &cloudflare.Client{Token: "cf-token", WorkerName: "inbox-worker", Base: fake.URL}}
	api := &cloudflare.API{Svc: svc}
	mapi := &mail.API{Store: db.Store, Zones: svc}
	asvc := &auth.Service{Store: db.Store, RateLimit: 1000, RateBurst: 1000}
	r := chi.NewRouter()
	asvc.Routes(r, func(r chi.Router) { api.Routes(r); mapi.Routes(r) })
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &env{db: db, fake: fake, svc: svc, srv: srv, ev: ev}
}

func (e *env) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+"/v1"+path, rd)
	req.Header.Set("Authorization", "Bearer "+e.db.Token)
	if method != http.MethodGet {
		req.Header.Set("Idempotency-Key", uuid.NewString())
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return resp.StatusCode, m
}

func routedZone(id, name string) cftest.Zone {
	return cftest.Zone{ID: id, Name: name, RoutingEnabled: true, CatchAllType: "worker", CatchAllValue: "inbox-worker", CatchAllOn: true}
}

func TestRoutingStates(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	cases := map[string]struct {
		z    cftest.Zone
		want string
	}{
		"worker":            {routedZone("z1", "a.com"), cloudflare.RoutingOK},
		"other worker":      {cftest.Zone{ID: "z2", Name: "b.com", RoutingEnabled: true, CatchAllType: "worker", CatchAllValue: "someone-else", CatchAllOn: true}, cloudflare.RoutingNone},
		"forward":           {cftest.Zone{ID: "z3", Name: "c.com", RoutingEnabled: true, CatchAllType: "forward", CatchAllValue: "me@gmail.com", CatchAllOn: true}, cloudflare.RoutingToEmail},
		"catch-all off":     {cftest.Zone{ID: "z4", Name: "d.com", RoutingEnabled: true, CatchAllType: "worker", CatchAllValue: "inbox-worker", CatchAllOn: false}, cloudflare.RoutingNone},
		"drop":              {cftest.Zone{ID: "z5", Name: "e.com", RoutingEnabled: true, CatchAllType: "drop", CatchAllOn: true}, cloudflare.RoutingNone},
		"no catch-all rule": {cftest.Zone{ID: "z6", Name: "f.com", RoutingEnabled: true}, cloudflare.RoutingNone},
		"routing disabled":  {cftest.Zone{ID: "z7", Name: "g.com", RoutingEnabled: false, CatchAllType: "worker", CatchAllValue: "inbox-worker", CatchAllOn: true}, cloudflare.RoutingNone},
	}
	for name, tc := range cases {
		e.fake.AddZone(tc.z)
		got, err := e.svc.Client.RoutingState(ctx, tc.z.ID)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q err %v, want %q", name, got, err, tc.want)
		}
	}
	// A rejected token is distinguishable from an outage.
	bad := &cloudflare.Client{Token: "wrong", Base: e.fake.URL}
	if _, err := bad.RoutingState(ctx, "z1"); err != cloudflare.ErrUnauthorized {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
	e.fake.FailNext("/zones/z1", 500)
	if _, err := e.svc.Client.RoutingState(ctx, "z1"); err == nil || err == cloudflare.ErrUnauthorized {
		t.Fatalf("500 must be a plain error, got %v", err)
	}
}

func TestDomainsListWithPagination(t *testing.T) {
	e := setup(t)
	e.fake.PerPage = 2
	e.fake.AddZone(routedZone("z1", "acme.com"))
	e.fake.AddZone(cftest.Zone{ID: "z2", Name: "bad.com", RoutingEnabled: true, CatchAllType: "forward", CatchAllValue: "x@y.com", CatchAllOn: true})
	e.fake.AddZone(routedZone("z3", "zeta.com"))
	e.fake.AddZone(cftest.Zone{ID: "z4", Name: "new.com", Status: "pending"})
	e.fake.AddZone(routedZone("z5", "mine.com"))
	if _, err := e.db.Store.CreateBusiness(context.Background(), store.Business{ID: ids.NewV7(), Name: "Mine", Domain: "mine.com", CFZoneID: "z5", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s, m := e.do(t, "GET", "/cloudflare/domains", nil)
	if s != 200 {
		t.Fatalf("%d %v", s, m)
	}
	got := map[string]map[string]any{}
	var order []string
	for _, d := range m["domains"].([]any) {
		d := d.(map[string]any)
		got[d["domain"].(string)] = d
		order = append(order, d["domain"].(string))
	}
	if strings.Join(order, ",") != "acme.com,bad.com,mine.com,new.com,zeta.com" {
		t.Fatalf("all pages, sorted: %v", order)
	}
	check := func(domain, routing string, selectable, added bool) {
		d := got[domain]
		if d["routing"] != routing || d["selectable"] != selectable || d["already_added"] != added {
			t.Errorf("%s: %v", domain, d)
		}
	}
	check("acme.com", "ok", true, false)
	check("bad.com", "to_email", false, false)
	check("mine.com", "ok", false, true)
	check("new.com", "unknown", false, false) // pending zone: not checked
	if got["acme.com"]["zone_id"] != "z1" || got["new.com"]["status"] != "pending" {
		t.Fatalf("%v", got)
	}

	// Fix routing in Cloudflare, check that one zone, it becomes selectable.
	e.fake.Set("z2", func(z *cftest.Zone) { z.CatchAllType, z.CatchAllValue = "worker", "inbox-worker" })
	s, m = e.do(t, "POST", "/cloudflare/domains/z2:check", nil)
	if s != 200 || m["routing"] != "ok" || m["selectable"] != true || m["domain"] != "bad.com" {
		t.Fatalf("check: %d %v", s, m)
	}
	if s, m := e.do(t, "POST", "/cloudflare/domains/nope:check", nil); s != 404 {
		t.Fatalf("unknown zone: %d %v", s, m)
	}
}

func TestBusinessCreateValidatesDomainAndStoresRouting(t *testing.T) {
	e := setup(t)
	e.fake.AddZone(routedZone("z1", "acme.com"))
	e.fake.AddZone(cftest.Zone{ID: "z2", Name: "bad.com", RoutingEnabled: true, CatchAllType: "forward", CatchAllValue: "x@y.com", CatchAllOn: true})

	s, m := e.do(t, "POST", "/businesses", map[string]any{"name": "Acme", "domain": "ACME.com"})
	if s != 201 || m["routing"] != "ok" {
		t.Fatalf("create: %d %v", s, m)
	}
	var zone string
	_ = e.db.Pool.QueryRow(context.Background(), `SELECT cf_zone_id FROM businesses WHERE domain='acme.com'`).Scan(&zone)
	if zone != "z1" {
		t.Fatalf("zone id %q", zone)
	}
	// Not routed yet: the business is created, with the problem visible.
	if s, m := e.do(t, "POST", "/businesses", map[string]any{"name": "Bad", "domain": "bad.com"}); s != 201 || m["routing"] != "to_email" {
		t.Fatalf("%d %v", s, m)
	}
	// A domain outside the account is refused.
	if s, m := e.do(t, "POST", "/businesses", map[string]any{"name": "X", "domain": "other.org"}); s != 422 || m["code"] != "domain_not_in_cloudflare" {
		t.Fatalf("%d %v", s, m)
	}
	// Cloudflare down: 502, nothing created.
	e.fake.FailNext("/zones", 500)
	if s, m := e.do(t, "POST", "/businesses", map[string]any{"name": "Y", "domain": "acme.com"}); s != 502 || m["code"] != "cloudflare_unavailable" {
		t.Fatalf("%d %v", s, m)
	}
}

func TestBusinessRoutingCheckUpdatesAndAnnounces(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.fake.AddZone(routedZone("z1", "acme.com"))
	_, m := e.do(t, "POST", "/businesses", map[string]any{"name": "Acme", "domain": "acme.com"})
	id := m["id"].(string)

	s, m := e.do(t, "POST", "/businesses/"+id+"/routing:check", nil)
	if s != 200 || m["routing"] != "ok" || m["checked_at"] == nil {
		t.Fatalf("%d %v", s, m)
	}
	if e.ev.count(events.TypeHealthChanged) != 0 {
		t.Fatal("no change, no event")
	}
	// The catch-all is switched back to "send to email" in Cloudflare.
	e.fake.Set("z1", func(z *cftest.Zone) { z.CatchAllType, z.CatchAllValue = "forward", "me@gmail.com" })
	s, m = e.do(t, "POST", "/businesses/"+id+"/routing:check", nil)
	if s != 200 || m["routing"] != "to_email" {
		t.Fatalf("%d %v", s, m)
	}
	if e.ev.count(events.TypeHealthChanged) != 1 {
		t.Fatalf("health.changed events: %d", e.ev.count(events.TypeHealthChanged))
	}
	var routing string
	var checked bool
	_ = e.db.Pool.QueryRow(ctx, `SELECT routing::text, routing_checked_at IS NOT NULL FROM businesses WHERE id=$1`, id).Scan(&routing, &checked)
	if routing != "to_email" || !checked {
		t.Fatalf("stored %s %v", routing, checked)
	}
	_, list := e.do(t, "GET", "/businesses", nil)
	if list["businesses"].([]any)[0].(map[string]any)["routing"] != "to_email" {
		t.Fatalf("list: %v", list)
	}

	// A business created without a zone id (older rows) learns it on the first check.
	b, err := e.db.Store.CreateBusiness(ctx, store.Business{ID: ids.NewV7(), Name: "Old", Domain: "old.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	e.fake.AddZone(routedZone("z9", "old.com"))
	if s, m := e.do(t, "POST", "/businesses/"+b.ID.String()+"/routing:check", nil); s != 200 || m["routing"] != "ok" {
		t.Fatalf("%d %v", s, m)
	}
	var z string
	_ = e.db.Pool.QueryRow(ctx, `SELECT cf_zone_id FROM businesses WHERE id=$1`, b.ID).Scan(&z)
	if z != "z9" {
		t.Fatalf("zone %q", z)
	}
	// Domain not in the account.
	b2, _ := e.db.Store.CreateBusiness(ctx, store.Business{ID: ids.NewV7(), Name: "Gone", Domain: "gone.com", Enabled: true})
	if s, m := e.do(t, "POST", "/businesses/"+b2.ID.String()+"/routing:check", nil); s != 409 || m["code"] != "cloudflare_zone_unknown" {
		t.Fatalf("%d %v", s, m)
	}
	if s, _ := e.do(t, "POST", "/businesses/"+uuid.NewString()+"/routing:check", nil); s != 404 {
		t.Fatalf("unknown business: %d", s)
	}

	// CheckAll skips disabled businesses and tolerates failures.
	e.fake.FailNext("/zones/z9", 500)
	before := e.fake.Count("GET /zones/z1")
	if err := e.svc.CheckAll(ctx); err != nil {
		t.Fatal(err)
	}
	if e.fake.Count("GET /zones/z1") == before {
		t.Fatal("CheckAll did not check the first business")
	}
}

func TestNotConfigured(t *testing.T) {
	db := testutil.NewDB(t)
	svc := &cloudflare.Service{Store: db.Store, Client: &cloudflare.Client{}}
	asvc := &auth.Service{Store: db.Store, RateLimit: 1000, RateBurst: 1000}
	r := chi.NewRouter()
	mapi := &mail.API{Store: db.Store, Zones: svc}
	asvc.Routes(r, func(r chi.Router) { (&cloudflare.API{Svc: svc}).Routes(r); mapi.Routes(r) })
	srv := httptest.NewServer(r)
	defer srv.Close()
	e := &env{db: db, srv: srv}
	if s, m := e.do(t, "GET", "/cloudflare/domains", nil); s != 503 || m["code"] != "cloudflare_not_configured" {
		t.Fatalf("%d %v", s, m)
	}
	// Business creation still works without Cloudflare (routing stays unknown).
	if s, m := e.do(t, "POST", "/businesses", map[string]any{"name": "Acme", "domain": "acme.com"}); s != 201 || m["routing"] != "unknown" {
		t.Fatalf("%d %v", s, m)
	}
}
