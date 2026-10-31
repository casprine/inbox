// Package gmailtest is a fake Google (OAuth + Gmail REST) for tests and local end-to-end runs.
package gmailtest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
)

// Failure is a scripted response for the next matching request.
type Failure struct {
	Status     int
	Body       string // JSON; defaults to a Google-style error body
	RetryAfter string
	// Skip lets this many matching requests through before the failure fires.
	Skip int
}

// Fake is an httptest server speaking enough of Google's OAuth and Gmail APIs.
type Fake struct {
	*httptest.Server

	mu sync.Mutex
	// Account state.
	Email        string
	RefreshToken string            // accepted refresh token; empty after Revoke
	GrantRevoked bool              // refresh answers invalid_grant
	codes        map[string]string // code -> PKCE challenge
	Revoked      []string
	// Counters (atomic reads via methods).
	tokenCalls atomic.Int64
	// Scripted failures keyed by "METHOD path-prefix" (consumed once each, FIFO).
	failures map[string][]Failure
	// Gmail data (see gmail.go in this package).
	gm gmailData
	// Requests is a log of "METHOD path" for assertions.
	Requests []string
}

// New starts a fake. Close it with Server.Close.
func New() *Fake {
	f := &Fake{Email: "sam@gmail.com", RefreshToken: "refresh-1", codes: map[string]string{}, failures: map[string][]Failure{}}
	f.gm.init()
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// AuthURL / TokenURL / RevokeURL / APIBase are the endpoints to put in gmail.Config.
func (f *Fake) AuthURL() string   { return f.URL + "/auth" }
func (f *Fake) TokenURL() string  { return f.URL + "/token" }
func (f *Fake) RevokeURL() string { return f.URL + "/revoke" }
func (f *Fake) APIBase() string   { return f.URL + "/gmail/v1" }

// IssueCode registers an authorization code bound to the PKCE challenge the app sent.
func (f *Fake) IssueCode(code, challenge string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codes[code] = challenge
}

// RevokeGrant makes every refresh answer invalid_grant, as after the user removes access.
func (f *Fake) RevokeGrant() { f.mu.Lock(); f.GrantRevoked = true; f.mu.Unlock() }

// RestoreGrant undoes RevokeGrant.
func (f *Fake) RestoreGrant() { f.mu.Lock(); f.GrantRevoked = false; f.mu.Unlock() }

// TokenCalls is how many times /token was hit.
func (f *Fake) TokenCalls() int { return int(f.tokenCalls.Load()) }

// Fail queues a failure for the next request whose "METHOD path" starts with key (e.g. "POST /gmail/v1/users/me/messages/").
func (f *Fake) Fail(key string, fl ...Failure) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[key] = append(f.failures[key], fl...)
}

// Count returns how many logged requests start with prefix ("METHOD path").
func (f *Fake) Count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.Requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func (f *Fake) takeFailure(key string) (Failure, bool) {
	for k, q := range f.failures {
		if strings.HasPrefix(key, k) && len(q) > 0 {
			if q[0].Skip > 0 {
				q[0].Skip--
				continue
			}
			fl := q[0]
			f.failures[k] = q[1:]
			return fl, true
		}
	}
	return Failure{}, false
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func gErr(w http.ResponseWriter, code int, reason, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{
		"code": code, "message": msg, "errors": []map[string]string{{"reason": reason, "message": msg}},
	}})
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.Path
	f.mu.Lock()
	f.Requests = append(f.Requests, key)
	fl, scripted := f.takeFailure(key)
	f.mu.Unlock()
	if scripted {
		if fl.RetryAfter != "" {
			w.Header().Set("Retry-After", fl.RetryAfter)
		}
		if fl.Body != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(fl.Status)
			_, _ = w.Write([]byte(fl.Body))
			return
		}
		gErr(w, fl.Status, "scripted", "scripted failure")
		return
	}
	switch {
	case r.URL.Path == "/token" && r.Method == http.MethodPost:
		f.token(w, r)
	case r.URL.Path == "/revoke" && r.Method == http.MethodPost:
		f.mu.Lock()
		_ = r.ParseForm()
		f.Revoked = append(f.Revoked, r.PostForm.Get("token"))
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{})
	case strings.HasPrefix(r.URL.Path, "/gmail/v1/"):
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer at-") {
			gErr(w, http.StatusUnauthorized, "authError", "Invalid Credentials")
			return
		}
		f.gmail(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *Fake) token(w http.ResponseWriter, r *http.Request) {
	f.tokenCalls.Add(1)
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		challenge, ok := f.codes[r.PostForm.Get("code")]
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || challenge != base64.RawURLEncoding.EncodeToString(sum[:]) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "Bad Request"})
			return
		}
		delete(f.codes, r.PostForm.Get("code"))
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": "at-" + r.PostForm.Get("code"), "refresh_token": f.RefreshToken, "expires_in": 3600, "token_type": "Bearer",
		})
	case "refresh_token":
		if f.GrantRevoked || r.PostForm.Get("refresh_token") != f.RefreshToken {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "Token has been expired or revoked."})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"access_token": "at-refreshed", "expires_in": 3600, "token_type": "Bearer"})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
	}
}
