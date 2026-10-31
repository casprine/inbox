// Package contract checks that api/openapi.yaml and the registered chi routes agree.
// Every server task that adds a route must add it to the spec (and vice versa).
package contract

import (
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/casprine/inbox/server/internal/alfather"
	"github.com/casprine/inbox/server/internal/auth"
	"github.com/casprine/inbox/server/internal/backfill"
	"github.com/casprine/inbox/server/internal/cloudflare"
	"github.com/casprine/inbox/server/internal/events"
	"github.com/casprine/inbox/server/internal/gmail"
	"github.com/casprine/inbox/server/internal/health"
	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/ingest"
	"github.com/casprine/inbox/server/internal/mail"
	"github.com/casprine/inbox/server/internal/retention"
	"github.com/casprine/inbox/server/internal/rulesapi"
)

type noEnq struct{}

func (noEnq) EnqueueIngest(*http.Request, ingest.IngestArgs) error { return nil }

// buildRouter mounts every route group exactly as cmd/inboxd does, with nil/fake dependencies.
func buildRouter() http.Handler {
	svc := &auth.Service{}
	api := &mail.API{}
	rapi := &rulesapi.API{}
	gapi := &gmail.API{Svc: &gmail.Service{}}
	hub := &events.Hub{}
	return httpapi.New(nil,
		httpapi.WithRoutes(func(r chi.Router) {
			svc.Routes(r, func(r chi.Router) {
				api.Routes(r)
				rapi.Routes(r)
				gapi.Routes(r)
				(&cloudflare.API{Svc: &cloudflare.Service{}}).Routes(r)
				(&health.API{}).Routes(r)
				(&backfill.API{Svc: &backfill.Service{}}).Routes(r)
				(&retention.API{}).Routes(r)
				(&alfather.API{}).Routes(r)
				r.Get("/events", hub.ServeHTTP)
			})
		}),
		httpapi.WithRoutes(gapi.PublicRoutes),
		httpapi.WithRoutes((&alfather.API{}).MCPRoutes),
		httpapi.WithRoutes(func(r chi.Router) { ingest.Routes(r, "secret", noEnq{}) }),
	)
}

func registered(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := chi.Walk(buildRouter().(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func specRoutes(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	methods := map[string]bool{"get": true, "put": true, "post": true, "delete": true, "patch": true, "head": true, "options": true}
	out := map[string]bool{}
	for p, item := range doc.Paths {
		for k := range item {
			if methods[k] {
				out[strings.ToUpper(k)+" "+p] = true
			}
		}
	}
	return out
}

func diff(a, b map[string]bool) []string {
	var d []string
	for k := range a {
		if !b[k] {
			d = append(d, k)
		}
	}
	sort.Strings(d)
	return d
}

func TestRoutesMatchOpenAPI(t *testing.T) {
	reg, spec := registered(t), specRoutes(t)
	if len(reg) < 20 {
		t.Fatalf("suspiciously few registered routes: %d", len(reg))
	}
	if d := diff(reg, spec); len(d) > 0 {
		t.Errorf("routes registered but missing from api/openapi.yaml:\n  %s", strings.Join(d, "\n  "))
	}
	if d := diff(spec, reg); len(d) > 0 {
		t.Errorf("operations in api/openapi.yaml with no registered route:\n  %s", strings.Join(d, "\n  "))
	}
}
