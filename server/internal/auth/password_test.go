package auth_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/casprine/inbox/server/internal/auth"
)

func TestPasswordVerifier(t *testing.T) {
	v, err := auth.NewPasswordVerifier("a long enough secret")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Verify("a long enough secret") {
		t.Fatal("correct password rejected")
	}
	for _, bad := range []string{"", "a long enough secre", "a long enough secret ", "A long enough secret"} {
		if v.Verify(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	// A second verifier uses a different random salt but accepts the same password.
	v2, _ := auth.NewPasswordVerifier("a long enough secret")
	if !v2.Verify("a long enough secret") {
		t.Fatal("second verifier rejects the password")
	}
}

func TestLoginLimiterBackoffEscalates(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := &auth.LoginLimiter{FreeFailures: 2, BaseDelay: 10 * time.Second, MaxDelay: 60 * time.Second,
		GlobalPerSecond: 1000, GlobalBurst: 1000, Now: func() time.Time { return now }}
	const ip = "203.0.113.7"
	attempt := func() (time.Duration, bool) { return l.Begin(ip) }

	for i := 0; i < 2; i++ { // free failures
		if _, ok := attempt(); !ok {
			t.Fatalf("free attempt %d refused", i)
		}
	}
	// Third attempt is allowed and starts a 10 s lock; the next ones double: 10, 20, 40, then capped at 60.
	for i, want := range []time.Duration{10, 20, 40, 60, 60} {
		wait, ok := attempt()
		if !ok {
			t.Fatalf("attempt after lock %d refused (wait %v)", i, wait)
		}
		wait, ok = attempt() // while locked
		if ok || wait != want*time.Second {
			t.Fatalf("step %d: ok=%v wait=%v, want lock of %v", i, ok, wait, want*time.Second)
		}
		now = now.Add(want * time.Second)
	}
	// Another client is unaffected.
	if _, ok := l.Begin("198.51.100.1"); !ok {
		t.Fatal("other client locked")
	}
	// Success clears the history.
	l.Succeed(ip)
	if _, ok := attempt(); !ok {
		t.Fatal("still locked after success")
	}
}

func TestLoginLimiterGlobalCap(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := &auth.LoginLimiter{FreeFailures: 1000, GlobalPerSecond: 1, GlobalBurst: 3, Now: func() time.Time { return now }}
	for i := 0; i < 3; i++ { // different clients, so the per-client limit never applies
		if _, ok := l.Begin("10.0.0." + string(rune('1'+i))); !ok {
			t.Fatalf("attempt %d refused", i)
		}
	}
	wait, ok := l.Begin("10.0.0.9")
	if ok || wait < time.Second {
		t.Fatalf("global cap not applied: ok=%v wait=%v", ok, wait)
	}
	now = now.Add(2 * time.Second)
	if _, ok := l.Begin("10.0.0.9"); !ok {
		t.Fatal("cap did not recover")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "192.0.2.1:5555"
	if got := auth.ClientIP(r, ""); got != "192.0.2.1" {
		t.Fatalf("conn addr: %q", got)
	}
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 100.64.0.9")
	if got := auth.ClientIP(r, "X-Forwarded-For"); got != "100.64.0.9" {
		t.Fatalf("header: %q (the last entry is the trusted proxy's)", got)
	}
	if got := auth.ClientIP(r, ""); got != "192.0.2.1" {
		t.Fatalf("header ignored when not configured: %q", got)
	}
}
