package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/time/rate"
)

// MinPasswordLength is the shortest OWNER_PASSWORD the server accepts at startup.
const MinPasswordLength = 12

// argon2id parameters (RFC 9106 second recommendation, scaled down a little for a small home server):
// about 20 ms and 64 MiB per attempt on a Mac mini.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
	// maxVerifies bounds concurrent argon2 runs so a burst of logins cannot exhaust memory.
	maxVerifies = 4
)

// PasswordVerifier checks login passwords against the owner password. The password is hashed once, in
// memory, with a random salt at construction; the plain text is not kept and nothing is persisted.
type PasswordVerifier struct {
	salt, hash []byte
	sem        chan struct{}
	// params can be lowered by tests to keep them quick.
	time, memory uint32
	threads      uint8
}

// NewPasswordVerifier hashes the owner password with argon2id and a fresh random salt.
// It fails when the password is shorter than MinPasswordLength characters.
func NewPasswordVerifier(password string) (*PasswordVerifier, error) {
	return newPasswordVerifier(password, argonTime, argonMemory, argonThreads)
}

func newPasswordVerifier(password string, t, m uint32, p uint8) (*PasswordVerifier, error) {
	if password == "" {
		return nil, errors.New("OWNER_PASSWORD is not set")
	}
	if n := utf8.RuneCountInString(password); n < MinPasswordLength {
		return nil, fmt.Errorf("OWNER_PASSWORD must be at least %d characters", MinPasswordLength)
	}
	v := &PasswordVerifier{salt: make([]byte, saltLen), sem: make(chan struct{}, maxVerifies), time: t, memory: m, threads: p}
	if _, err := rand.Read(v.salt); err != nil {
		return nil, err
	}
	v.hash = argon2.IDKey([]byte(password), v.salt, t, m, p, argonKeyLen)
	return v, nil
}

// Verify reports whether candidate equals the owner password, in constant time over the derived key.
func (v *PasswordVerifier) Verify(candidate string) bool {
	v.sem <- struct{}{}
	defer func() { <-v.sem }()
	got := argon2.IDKey([]byte(candidate), v.salt, v.time, v.memory, v.threads, argonKeyLen)
	return subtle.ConstantTimeCompare(got, v.hash) == 1
}

// LoginLimiter throttles POST /v1/auth/login: an escalating per-client backoff plus a global cap on
// attempts per second, so a distributed guess cannot get around the per-client limit.
//
// Every attempt is registered as a failure when it starts and cleared on success, so a burst of
// parallel guesses cannot all slip through before the first failure is recorded.
type LoginLimiter struct {
	// FreeFailures is how many consecutive failures a client may make before backoff starts (default 4).
	FreeFailures int
	// BaseDelay is the lock after the first failure past FreeFailures; it doubles per failure (default 5s).
	BaseDelay time.Duration
	// MaxDelay caps the lock (default 15 minutes).
	MaxDelay time.Duration
	// Forget drops a client's history after this long without attempts (default 1 hour).
	Forget time.Duration
	// GlobalPerSecond / GlobalBurst cap login attempts across all clients (defaults 1 per second, burst 10).
	GlobalPerSecond float64
	GlobalBurst     int
	// Now is replaceable in tests.
	Now func() time.Time

	mu      sync.Mutex
	clients map[string]*clientState
	global  *rate.Limiter
	sweepAt time.Time
}

type clientState struct {
	fails    int
	lockedTo time.Time
	last     time.Time
}

func (l *LoginLimiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func orDur(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Begin registers an attempt from client. When it returns ok=false the caller answers 429 with
// Retry-After: retryAfter and must not check the password. When ok, the caller must call Succeed if the
// password was right.
func (l *LoginLimiter) Begin(client string) (retryAfter time.Duration, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.clients == nil {
		l.clients = map[string]*clientState{}
	}
	if l.global == nil {
		per, burst := l.GlobalPerSecond, l.GlobalBurst
		if per <= 0 {
			per = 1
		}
		if burst <= 0 {
			burst = 10
		}
		l.global = rate.NewLimiter(rate.Limit(per), burst)
	}
	l.sweep(now)
	st := l.clients[client]
	if st == nil {
		st = &clientState{}
		l.clients[client] = st
	}
	if now.Before(st.lockedTo) {
		return st.lockedTo.Sub(now), false
	}
	res := l.global.ReserveN(now, 1)
	if d := res.DelayFrom(now); !res.OK() || d > 0 {
		res.CancelAt(now)
		return max(d, time.Second), false
	}
	st.last = now
	st.fails++
	if free := orInt(l.FreeFailures, 4); st.fails > free {
		over := st.fails - free // 1 for the first failure past the free ones
		maxD := orDur(l.MaxDelay, 15*time.Minute)
		d := maxD
		if over < 40 { // avoid overflow of the shift
			d = min(orDur(l.BaseDelay, 5*time.Second)<<(over-1), maxD)
			if d <= 0 {
				d = maxD
			}
		}
		st.lockedTo = now.Add(d)
	}
	return 0, true
}

func orInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// Succeed clears the client's failures after a correct password.
func (l *LoginLimiter) Succeed(client string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.clients, client)
}

func (l *LoginLimiter) sweep(now time.Time) {
	if now.Before(l.sweepAt) {
		return
	}
	l.sweepAt = now.Add(time.Minute)
	forget := orDur(l.Forget, time.Hour)
	for k, st := range l.clients {
		if now.Sub(st.last) > forget && now.After(st.lockedTo) {
			delete(l.clients, k)
		}
	}
}

// ClientIP names the caller for rate limiting. With header empty it is the connection's address. With a
// header (for example X-Forwarded-For, set by a proxy the operator trusts) the last entry, the one that
// proxy appended, is used; a missing header falls back to the connection address.
func ClientIP(r *http.Request, header string) string {
	if header != "" {
		if v := r.Header.Get(header); v != "" {
			parts := strings.Split(v, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
