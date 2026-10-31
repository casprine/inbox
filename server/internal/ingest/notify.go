// Package ingest receives edge notifications and turns raw blobs into messages.
package ingest

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	maxSkew     = 5 * time.Minute
	maxBodySize = 64 << 10
)

// Sign returns the hex HMAC-SHA256 of ts + "." + body (the edge worker's signature format).
func Sign(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// VerifyHMAC rejects requests without a fresh, correctly signed body. The body is
// restored for the next handler.
func VerifyHMAC(secret string, now func() time.Time) func(http.Handler) http.Handler {
	if now == nil {
		now = time.Now
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodySize))
			if err != nil {
				problem(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body too large")
				return
			}
			ts := r.Header.Get("X-Inbox-Timestamp")
			sec, err := strconv.ParseInt(ts, 10, 64)
			if err != nil {
				problem(w, http.StatusUnauthorized, "invalid_signature", "missing or invalid timestamp")
				return
			}
			if d := now().Sub(time.Unix(sec, 0)); d > maxSkew || d < -maxSkew {
				problem(w, http.StatusUnauthorized, "invalid_signature", "timestamp outside allowed window")
				return
			}
			got, err := hex.DecodeString(strings.TrimSpace(r.Header.Get("X-Inbox-Signature")))
			want, _ := hex.DecodeString(Sign(secret, ts, body))
			if err != nil || !hmac.Equal(got, want) {
				problem(w, http.StatusUnauthorized, "invalid_signature", "bad signature")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	}
}

// NotifyPayload is the edge worker's JSON (DECISIONS D-010).
type NotifyPayload struct {
	Key        *string `json:"key"`
	Reason     string  `json:"reason,omitempty"`
	EnvFrom    string  `json:"env_from"`
	EnvTo      string  `json:"env_to"`
	RawSize    int64   `json:"raw_size"`
	ReceivedAt string  `json:"received_at"`
	MessageID  string  `json:"message_id"`
}

// Enqueuer schedules ingest work; implementations must be idempotent per Args.
type Enqueuer interface {
	EnqueueIngest(r *http.Request, a IngestArgs) error
}

// NotifyHandler serves POST /ingest/v1/notify. Mount it behind VerifyHMAC.
func NotifyHandler(enq Enqueuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			problem(w, http.StatusBadRequest, "invalid_body", "cannot read body")
			return
		}
		var p NotifyPayload
		if err := json.Unmarshal(body, &p); err != nil {
			problem(w, http.StatusBadRequest, "invalid_body", "body is not valid JSON")
			return
		}
		args := IngestArgs{EnvFrom: p.EnvFrom, EnvTo: p.EnvTo, RawSize: p.RawSize, ReceivedAt: p.ReceivedAt, MessageID: p.MessageID}
		switch {
		case p.Key != nil && strings.HasPrefix(*p.Key, "raw/"):
			args.Key = *p.Key
		case p.Key == nil && p.Reason == "store_failed":
			sum := sha256.Sum256(body)
			args.Reason = p.Reason
			args.FailID = hex.EncodeToString(sum[:8])
		default:
			problem(w, http.StatusBadRequest, "invalid_payload", "key must start with raw/ or reason must be store_failed")
			return
		}
		if err := enq.EnqueueIngest(r, args); err != nil {
			problem(w, http.StatusServiceUnavailable, "enqueue_failed", "could not queue job")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}` + "\n"))
	}
}

func problem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "detail": detail,
	})
}

// Routes mounts POST /ingest/v1/notify behind the HMAC check.
func Routes(r chi.Router, secret string, enq Enqueuer) {
	r.With(VerifyHMAC(secret, nil)).Post("/ingest/v1/notify", NotifyHandler(enq))
}

// ListenerHandler is the whole handler of the dedicated ingest listener (INGEST_ADDR, published with
// Tailscale Funnel): POST /ingest/v1/notify behind the HMAC and timestamp checks and nothing else, so
// the public address exposes no part of the app API. Every other path answers 404.
func ListenerHandler(secret string, enq Enqueuer) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	Routes(r, secret, enq)
	return r
}
