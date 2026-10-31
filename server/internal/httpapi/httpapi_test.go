package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
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
