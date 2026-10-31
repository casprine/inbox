package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/casprine/inbox/server/internal/httpapi"
)

type stub struct {
	err   error
	calls int
}

func (s *stub) Ping(context.Context) error { s.calls++; return s.err }

func TestReadinessNamesTheFailingComponentAndCachesBlob(t *testing.T) {
	db, bl := &stub{}, &stub{}
	r := &readiness{db: db, blob: bl, ttl: time.Hour}
	for i := 0; i < 3; i++ {
		if err := r.Ping(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if bl.calls != 1 {
		t.Fatalf("blob pinged %d times, want 1 (cached)", bl.calls)
	}
	r = &readiness{db: db, blob: &stub{err: errors.New("403 secret-host")}, ttl: time.Hour}
	var f httpapi.Failure
	if err := r.Ping(context.Background()); !errors.As(err, &f) || f.Component() != "blob store" {
		t.Fatalf("blob failure: %v", err)
	}
	r = &readiness{db: &stub{err: errors.New("down")}, blob: bl, ttl: time.Hour}
	if err := r.Ping(context.Background()); !errors.As(err, &f) || f.Component() != "database" {
		t.Fatalf("db failure: %v", err)
	}
}
