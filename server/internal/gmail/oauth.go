package gmail

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/store"
)

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// Connect starts the OAuth handshake and returns the Google consent URL.
func (s *Service) Connect(ctx context.Context, device *uuid.UUID) (string, error) {
	if !s.Configured() {
		return "", ErrNotConfigured
	}
	state, err := randomString(24)
	if err != nil {
		return "", err
	}
	verifier, err := randomString(48)
	if err != nil {
		return "", err
	}
	if err := s.Store.CreateGmailOAuthState(ctx, state, device, verifier, stateTTL); err != nil {
		return "", err
	}
	q := url.Values{
		"client_id":             {s.Cfg.ClientID},
		"redirect_uri":          {s.Cfg.RedirectURL},
		"response_type":         {"code"},
		"scope":                 {strings.Join(s.Cfg.scopes(), " ")},
		"state":                 {state},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
		"access_type":           {"offline"}, // refresh token
		"prompt":                {"consent"}, // always return a refresh token, even on reconnect
	}
	u := s.Cfg.authURL()
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + q.Encode(), nil
}

// CallbackResult is where to send the user's browser after the callback.
type CallbackResult struct {
	Status string // ok | error
	Reason string // set when Status is error
}

// Redirect is the deep link for the app.
func (r CallbackResult) Redirect() string {
	q := url.Values{"status": {r.Status}}
	if r.Reason != "" {
		q.Set("reason", r.Reason)
	}
	return DeepLink + "?" + q.Encode()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// HandleCallback finishes the handshake: validates state, exchanges the code (with PKCE),
// reads the account email, stores the encrypted refresh token and resumes paused write-backs.
// googleErr is the `error` query parameter Google sends when the user declines.
func (s *Service) HandleCallback(ctx context.Context, code, state, googleErr string) CallbackResult {
	fail := func(reason string, err error) CallbackResult {
		if err != nil {
			s.log().Warn("gmail callback failed", "reason", reason, "err", err)
		}
		return CallbackResult{Status: "error", Reason: reason}
	}
	if !s.Configured() {
		return fail("not_configured", nil)
	}
	if state == "" {
		return fail("invalid_state", nil)
	}
	verifier, err := s.Store.ConsumeGmailOAuthState(ctx, state)
	if errors.Is(err, store.ErrNotFound) {
		return fail("invalid_state", nil) // unknown, reused or expired
	}
	if err != nil {
		return fail("internal", err)
	}
	if googleErr != "" {
		if googleErr == "access_denied" {
			return fail("access_denied", nil)
		}
		return fail("google_error", nil)
	}
	if code == "" {
		return fail("invalid_request", nil)
	}
	tok, err := s.tokenRequest(ctx, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier},
		"redirect_uri": {s.Cfg.RedirectURL},
	})
	if err != nil {
		return fail("exchange_failed", err)
	}
	if tok.RefreshToken == "" {
		return fail("no_refresh_token", nil)
	}
	email, err := s.profileEmail(ctx, tok.AccessToken)
	if err != nil {
		return fail("profile_failed", err)
	}
	enc, err := s.Keys.Seal([]byte(tok.RefreshToken), []byte(tokenAAD))
	if err != nil {
		return fail("internal", err)
	}
	var acct store.GmailAccount
	err = s.Store.WithTx(ctx, func(q *store.Queries) error {
		var err error
		if acct, err = q.UpsertGmailAccount(ctx, ids.NewV7(), email, enc, s.Keys.Current); err != nil {
			return err
		}
		_, err = q.ResumePausedGmailSync(ctx)
		return err
	})
	if err != nil {
		return fail("internal", err)
	}
	s.cacheToken(acct.ID, tok)
	s.publishStatus(ctx)
	if s.OnConnected != nil {
		s.OnConnected(ctx)
	}
	return CallbackResult{Status: "ok"}
}

func (s *Service) cacheToken(account uuid.UUID, t tokenResponse) {
	if t.AccessToken == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.access == nil {
		s.access = map[string]cachedToken{}
	}
	s.access[account.String()] = cachedToken{t.AccessToken, time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)}
}

// tokenRequest posts to the token endpoint with the client credentials.
func (s *Service) tokenRequest(ctx context.Context, form url.Values) (tokenResponse, error) {
	form.Set("client_id", s.Cfg.ClientID)
	form.Set("client_secret", s.Cfg.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Cfg.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http().Do(req)
	if err != nil {
		return tokenResponse{}, httpapi.ScrubURL(err)
	}
	defer resp.Body.Close()
	var t tokenResponse
	_ = json.NewDecoder(resp.Body).Decode(&t)
	if resp.StatusCode != http.StatusOK || t.Error != "" {
		return t, &TokenError{Status: resp.StatusCode, Code: t.Error, Desc: t.ErrorDesc}
	}
	if t.AccessToken == "" {
		return t, errors.New("gmail: token endpoint returned no access token")
	}
	return t, nil
}

// TokenError is a failed token endpoint call.
type TokenError struct {
	Status int
	Code   string
	Desc   string
}

func (e *TokenError) Error() string {
	return fmt.Sprintf("gmail: token endpoint %d %s %s", e.Status, e.Code, e.Desc)
}

// profileEmail asks Gmail who the token belongs to.
func (s *Service) profileEmail(ctx context.Context, access string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Cfg.apiBase()+"/users/me/profile", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := s.http().Do(req)
	if err != nil {
		return "", httpapi.ScrubURL(err)
	}
	defer resp.Body.Close()
	var p struct {
		EmailAddress string `json:"emailAddress"`
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gmail: profile status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil || p.EmailAddress == "" {
		return "", errors.New("gmail: profile has no email")
	}
	return strings.ToLower(p.EmailAddress), nil
}
