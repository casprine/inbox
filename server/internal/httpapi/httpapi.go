// Package httpapi wires the HTTP router.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// Pinger reports whether a dependency is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Option customises the router.
type Option func(chi.Router)

// WithRoutes lets other packages mount their routes (e.g. the ingest endpoint).
func WithRoutes(f func(chi.Router)) Option { return f }

// MaxRequestBody caps every request body on the API listener (handlers set tighter limits of their own).
const MaxRequestBody = 4 << 20

// Failure is implemented by readiness errors that name the failing component ("database", "blob store") so
// /readyz can say which one without printing connection details.
type Failure interface{ Component() string }

// limitBody rejects declared oversize bodies early and caps undeclared (chunked) ones while they are read.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.ContentLength > MaxRequestBody {
			WriteProblem(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body too large")
			return
		}
		req.Body = http.MaxBytesReader(w, req.Body, MaxRequestBody)
		next.ServeHTTP(w, req)
	})
}

func New(db Pinger, opts ...Option) http.Handler {
	r := chi.NewRouter()
	r.Use(limitBody)
	for _, o := range opts {
		o(r)
	}
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		if db == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "failed": "database"})
			return
		}
		if err := db.Ping(ctx); err != nil {
			out := map[string]string{"status": "unavailable"}
			var f Failure
			if errors.As(err, &f) {
				out["failed"] = f.Component()
			}
			writeJSON(w, http.StatusServiceUnavailable, out)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	return r
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
