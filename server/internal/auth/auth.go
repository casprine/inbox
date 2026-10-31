// Package auth implements password sign-in, device tokens and request middleware.
package auth

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/casprine/inbox/server/internal/crypto"
	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/store"
)

const (
	idempotencyTTL = 24 * time.Hour
	// idempotencyInflight is how long an unfinished claim blocks retries before it counts as abandoned.
	idempotencyInflight = 2 * time.Minute
	touchEvery          = time.Minute
)

// Service holds auth dependencies.
type Service struct {
	Store *store.Store
	// Password checks the owner password for POST /v1/auth/login (required).
	Password *PasswordVerifier
	// Login throttles login attempts (nil: a limiter with the defaults is created on first use).
	Login *LoginLimiter
	// ClientIPHeader names a header set by a trusted reverse proxy that carries the caller's address
	// (for example X-Forwarded-For); empty uses the connection address.
	ClientIPHeader string
	Log            *slog.Logger
	// RateLimit/RateBurst configure the per-device token bucket (defaults 10/s, burst 30).
	RateLimit float64
	RateBurst int

	loginOnce sync.Once
	mu        sync.Mutex
	limiters  map[uuid.UUID]*rate.Limiter
	touched   map[uuid.UUID]time.Time
}

type ctxKey struct{}

// DeviceFrom returns the authenticated device from ctx.
func DeviceFrom(ctx context.Context) (store.Device, bool) {
	d, ok := ctx.Value(ctxKey{}).(store.Device)
	return d, ok
}

// Routes mounts the auth endpoints and a protected /v1 group; register is called
// with the group (Bearer + rate limit + idempotency applied) so later tasks add routes.
func (s *Service) Routes(r chi.Router, register func(chi.Router)) {
	r.Post("/v1/auth/login", s.handleLogin)
	r.Route("/v1", func(r chi.Router) {
		r.Use(s.Bearer, s.RateLimiter, s.Idempotency)
		r.Delete("/devices/{id}", s.handleRevoke)
		if register != nil {
			register(r)
		}
	})
}

type loginReq struct {
	Password   string `json:"password"`
	DeviceName string `json:"device_name"`
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	s.loginOnce.Do(func() {
		if s.Login == nil {
			s.Login = &LoginLimiter{}
		}
	})
	var req loginReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.Password == "" {
		httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_request", "password is required")
		return
	}
	client := ClientIP(r, s.ClientIPHeader)
	if wait, ok := s.Login.Begin(client); !ok {
		secs := int((wait + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
		httpapi.WriteProblem(w, http.StatusTooManyRequests, "rate_limited", "too many sign-in attempts; try again later")
		return
	}
	if s.Password == nil || !s.Password.Verify(req.Password) {
		// Deliberately generic: nothing says whether the password was close, or whether one is configured.
		httpapi.WriteProblem(w, http.StatusUnauthorized, "invalid_credentials", "incorrect password")
		return
	}
	s.Login.Succeed(client)
	token, err := crypto.NewToken()
	if err != nil {
		httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
		return
	}
	hash, _ := hex.DecodeString(crypto.HashToken(token))
	id := ids.NewV7()
	name := strings.TrimSpace(req.DeviceName)
	if len(name) > 100 {
		name = name[:100]
	}
	if err := s.Store.CreateDevice(r.Context(), id, name, hash); err != nil {
		s.log().Error("create device", "err", err)
		httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
		return
	}
	resp := map[string]string{"device_id": id.String(), "token": token}
	if step, err := s.setupStep(r.Context()); err != nil {
		s.log().Warn("setup step", "err", err) // optional field: omitted on error, the app falls back to its own guess
	} else {
		resp["setup_step"] = step
	}
	httpapi.WriteJSON(w, http.StatusOK, resp)
}

// setupStep says where first-run setup stands, from server state: "domains" while no business exists,
// "gmail" once one does but Gmail is not connected, "done" when Gmail is connected. Backfill is optional and
// does not gate "done" (the owner may skip importing old mail).
func (s *Service) setupStep(ctx context.Context) (string, error) {
	biz, gm, _, err := s.Store.SetupProgress(ctx)
	if err != nil {
		return "", err
	}
	switch {
	case !biz:
		return "domains", nil
	case !gm:
		return "gmail", nil
	}
	return "done", nil
}

// Bearer authenticates the device token and stores the device in the request context.
func (s *Service) Bearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		tok, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || tok == "" {
			unauthorized(w)
			return
		}
		hash, _ := hex.DecodeString(crypto.HashToken(tok))
		d, err := s.Store.DeviceByTokenHash(r.Context(), hash)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
				return
			}
			unauthorized(w)
			return
		}
		s.touch(r.Context(), d.ID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, d)))
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	httpapi.WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid device token")
}

func (s *Service) touch(ctx context.Context, id uuid.UUID) {
	s.mu.Lock()
	if s.touched == nil {
		s.touched = map[uuid.UUID]time.Time{}
	}
	last, ok := s.touched[id]
	due := !ok || time.Since(last) >= touchEvery
	if due {
		s.touched[id] = time.Now()
	}
	s.mu.Unlock()
	if due {
		if err := s.Store.TouchDevice(ctx, id); err != nil {
			s.log().Warn("touch device", "err", err)
		}
	}
}

// RateLimiter applies a per-device token bucket; must run after Bearer.
func (s *Service) RateLimiter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d, ok := DeviceFrom(r.Context())
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		s.mu.Lock()
		if s.limiters == nil {
			s.limiters = map[uuid.UUID]*rate.Limiter{}
		}
		l := s.limiters[d.ID]
		if l == nil {
			lim, burst := s.RateLimit, s.RateBurst
			if lim <= 0 {
				lim = 10
			}
			if burst <= 0 {
				burst = 30
			}
			l = rate.NewLimiter(rate.Limit(lim), burst)
			s.limiters[d.ID] = l
		}
		s.mu.Unlock()
		if !l.Allow() {
			w.Header().Set("Retry-After", "1")
			httpapi.WriteProblem(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type storedResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Body        []byte `json:"body"`
}

type recorder struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.buf.Len() < 1<<20 {
		r.buf.Write(b)
	}
	return r.ResponseWriter.Write(b)
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Idempotency replays the stored response for a repeated Idempotency-Key on
// mutating methods (same device, 24h). Responses with status >=500 are not stored.
func (s *Service) Idempotency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			key = ""
		}
		d, ok := DeviceFrom(r.Context())
		if key == "" || !ok {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > 255 {
			httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key too long")
			return
		}
		state, raw, err := s.Store.ClaimIdempotency(r.Context(), d.ID, key, idempotencyTTL, idempotencyInflight)
		if err != nil {
			httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
			return
		}
		switch state {
		case store.IdemInProgress:
			w.Header().Set("Retry-After", "1")
			httpapi.WriteProblem(w, http.StatusConflict, "request_in_progress", "a request with this Idempotency-Key is still running; retry in a moment")
			return
		case store.IdemReplay:
			var sr storedResponse
			if json.Unmarshal(raw, &sr) == nil && sr.Status != 0 {
				if sr.ContentType != "" {
					w.Header().Set("Content-Type", sr.ContentType)
				}
				w.Header().Set("Idempotency-Replayed", "true")
				w.WriteHeader(sr.Status)
				_, _ = w.Write(sr.Body)
				return
			}
			// unreadable stored response: treat the key as unusable rather than running the handler twice
			httpapi.WriteProblem(w, http.StatusConflict, "request_in_progress", "stored response unreadable")
			return
		}
		// We own the claim. Whatever happens (5xx, panic, response too large to store), release it unless a
		// response was stored, so the client's retry is not blocked until the claim goes stale.
		done := false
		bg := context.WithoutCancel(r.Context())
		defer func() {
			if !done {
				if err := s.Store.ReleaseIdempotency(bg, d.ID, key); err != nil {
					s.log().Warn("release idempotency claim", "err", err)
				}
			}
		}()
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		if rec.status < 500 && rec.buf.Len() < 1<<20 {
			sr := storedResponse{Status: rec.status, ContentType: w.Header().Get("Content-Type"), Body: rec.buf.Bytes()}
			if err := s.Store.PutIdempotentResponse(bg, d.ID, key, sr); err != nil {
				s.log().Warn("store idempotent response", "err", err)
			} else {
				done = true
			}
		}
	})
}

func (s *Service) handleRevoke(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_id", "device id must be a UUID")
		return
	}
	ok, err := s.Store.RevokeDevice(r.Context(), id)
	if err != nil {
		httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
		return
	}
	if !ok {
		httpapi.WriteProblem(w, http.StatusNotFound, "device_not_found", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}
