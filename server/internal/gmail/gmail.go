// Package gmail owns the Gmail connection: OAuth, token refresh, the REST client, the
// write-back engine, labels and filters.
package gmail

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/casprine/inbox/server/internal/crypto"
	"github.com/casprine/inbox/server/internal/events"
	"github.com/casprine/inbox/server/internal/store"
)

// Errors callers can branch on.
var (
	ErrNotConfigured = errors.New("gmail: oauth client not configured")
	ErrNoAccount     = errors.New("gmail: no account connected")
	ErrNeedsReauth   = errors.New("gmail: account needs re-authentication")
)

// Default Google endpoints; Config fields override them for fakes.
const (
	DefaultAuthURL   = "https://accounts.google.com/o/oauth2/v2/auth"
	DefaultTokenURL  = "https://oauth2.googleapis.com/token"
	DefaultRevokeURL = "https://oauth2.googleapis.com/revoke"
	DefaultAPIBase   = "https://gmail.googleapis.com/gmail/v1"
	// ScopeModify reads, labels and archives mail. Filters are read with the same scope;
	// if Google rejects that, set Config.Scopes to add gmail.settings.basic (DECISIONS Q-T18-2).
	ScopeModify = "https://www.googleapis.com/auth/gmail.modify"

	// tokenAAD is authenticated with the sealed refresh token (not stored).
	tokenAAD = "gmail_refresh_token"

	stateTTL = 10 * time.Minute
	// Deep link the app's ASWebAuthenticationSession listens for.
	DeepLink = "inbox://gmail/connected"
)

// Config configures the OAuth client and endpoints.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string // PUBLIC_BASE_URL + /oauth/google/callback
	Scopes       []string
	AuthURL      string
	TokenURL     string
	RevokeURL    string
	APIBase      string
}

func (c Config) authURL() string   { return or(c.AuthURL, DefaultAuthURL) }
func (c Config) tokenURL() string  { return or(c.TokenURL, DefaultTokenURL) }
func (c Config) revokeURL() string { return or(c.RevokeURL, DefaultRevokeURL) }
func (c Config) apiBase() string   { return strings.TrimRight(or(c.APIBase, DefaultAPIBase), "/") }
func (c Config) scopes() []string {
	if len(c.Scopes) > 0 {
		return c.Scopes
	}
	return []string{ScopeModify}
}

func or(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// Service is the Gmail account manager.
type Service struct {
	Store  *store.Store
	Keys   *crypto.Keyring
	Cfg    Config
	Events events.Publisher // optional
	HTTP   *http.Client     // optional; defaults to a client with a 30s timeout
	Log    *slog.Logger

	// OnConnected runs after a successful connect (the write-back engine uses it to wake due rows).
	OnConnected func(ctx context.Context)

	mu     sync.Mutex
	access map[string]cachedToken // by account id
	lim    *rate.Limiter
}

type cachedToken struct {
	token   string
	expires time.Time
}

// Configured reports whether the OAuth client id/secret and the token key are present.
func (s *Service) Configured() bool {
	return s != nil && s.Cfg.ClientID != "" && s.Cfg.ClientSecret != "" && s.Keys != nil
}

func (s *Service) http() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) publishStatus(ctx context.Context) {
	if s.Events == nil {
		return
	}
	st, err := s.Status(ctx)
	if err != nil {
		return
	}
	s.Events.Publish(events.New(events.TypeGmailStatus, map[string]any{
		"state": st.State, "email": st.Email, "waiting": st.Waiting, "rate_limited_until": st.RateLimitedUntil,
	}))
}

// Status is the account summary served by GET /v1/gmail/status.
type Status struct {
	State       string // not_connected | connected | needs_reauth
	Email       *string
	ConnectedAt *time.Time
	LastWriteAt *time.Time
	Waiting     int
	// RateLimitedUntil is set while Gmail has told us to back off ("Paused by Gmail rate limits").
	RateLimitedUntil *time.Time
}

// Status reads the account row and the number of actions waiting for Gmail.
func (s *Service) Status(ctx context.Context) (Status, error) {
	waiting, err := s.Store.CountGmailWaiting(ctx)
	if err != nil {
		return Status{}, err
	}
	var until *time.Time
	if t := s.RateLimitedUntil(ctx); time.Now().Before(t) {
		until = &t
	}
	a, err := s.Store.GetGmailAccount(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return Status{State: "not_connected", Waiting: waiting, RateLimitedUntil: until}, nil
	}
	if err != nil {
		return Status{}, err
	}
	st := Status{State: a.Status, Email: &a.Email, ConnectedAt: &a.ConnectedAt, LastWriteAt: a.LastWriteAt, Waiting: waiting, RateLimitedUntil: until}
	if a.Status == "revoked" {
		st.State = "needs_reauth"
	}
	return st, nil
}
