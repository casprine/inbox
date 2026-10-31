package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestHealth(t *testing.T) {
	cases := []struct {
		name, path string
		db         Pinger
		want       int
	}{
		{"healthz", "/healthz", nil, 200},
		{"readyz ok", "/readyz", fakePinger{}, 200},
		{"readyz down", "/readyz", fakePinger{errors.New("x")}, 503},
		{"readyz nil", "/readyz", nil, 503},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			New(c.db).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
			if rec.Code != c.want {
				t.Fatalf("got %d want %d", rec.Code, c.want)
			}
		})
	}
}

type failingPinger struct{}

func (failingPinger) Ping(context.Context) error { return namedFailure{} }

type namedFailure struct{}

func (namedFailure) Error() string     { return "boom with secret-host" }
func (namedFailure) Component() string { return "blob store" }

func TestReadyzNamesTheFailedDependencyButNotItsDetails(t *testing.T) {
	srv := httptest.NewServer(New(failingPinger{}))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 503 || !strings.Contains(string(b), `"failed":"blob store"`) || strings.Contains(string(b), "secret-host") {
		t.Fatalf("readyz: %d %s", resp.StatusCode, b)
	}
}

func TestRequestBodyIsCapped(t *testing.T) {
	var read int64
	srv := httptest.NewServer(New(nil, WithRoutes(func(r chi.Router) {
		r.Post("/echo", func(w http.ResponseWriter, req *http.Request) {
			n, err := io.Copy(io.Discard, req.Body)
			read = n
			if err != nil {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
			}
		})
	})))
	defer srv.Close()
	big := strings.NewReader(strings.Repeat("x", MaxRequestBody+10))
	resp, err := http.Post(srv.URL+"/echo", "text/plain", big)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize body: %d (read %d)", resp.StatusCode, read)
	}
}
