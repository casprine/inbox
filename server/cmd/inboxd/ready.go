package main

import (
	"context"
	"sync"
	"time"
)

// componentError names the dependency that failed without carrying its connection details.
type componentError struct {
	name string
	err  error
}

func (e *componentError) Error() string     { return e.name + ": " + e.err.Error() }
func (e *componentError) Unwrap() error     { return e.err }
func (e *componentError) Component() string { return e.name }

type pinger interface{ Ping(context.Context) error }

// readiness is what GET /readyz asks: Postgres answers and the blob store (R2) answers. The blob result is
// cached briefly because the container health check calls /readyz every few seconds and each call would
// otherwise be a billable R2 request.
type readiness struct {
	db   pinger
	blob pinger // nil when ingest is not configured
	ttl  time.Duration

	mu      sync.Mutex
	checked time.Time
	blobErr error
}

func (r *readiness) Ping(ctx context.Context) error {
	if err := r.db.Ping(ctx); err != nil {
		return &componentError{"database", err}
	}
	if r.blob == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.checked.IsZero() && time.Since(r.checked) < r.ttl {
		if r.blobErr != nil {
			return &componentError{"blob store", r.blobErr}
		}
		return nil
	}
	err := r.blob.Ping(ctx)
	r.checked, r.blobErr = time.Now(), err
	if err != nil {
		return &componentError{"blob store", err}
	}
	return nil
}
