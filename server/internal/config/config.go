// Package config parses environment configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/casprine/inbox/server/internal/crypto"
)

// MinOwnerPassword is the shortest accepted OWNER_PASSWORD (characters).
const MinOwnerPassword = 12

type Config struct {
	DatabaseURL        string
	HTTPAddr           string
	PublicBaseURL      string
	IngestSecret       string
	S3Endpoint         string
	S3Bucket           string
	S3AccessKey        string
	S3SecretKey        string
	S3Region           string
	TokenEncKey        string // legacy single key (id k1)
	TokenEncKeys       string // id:base64,id:base64 - first seals, all open
	OwnerPassword      string // OWNER_PASSWORD: the one sign-in password (D-012); required to serve
	LoginIPHeader      string // LOGIN_IP_HEADER: header with the caller's address from a trusted proxy (optional)
	IngestAddr         string // INGEST_ADDR: second listener that serves only /ingest/v1/notify (default :8081)
	ReconcileInterval  time.Duration
	ReconcileMinAge    time.Duration // RECONCILE_MIN_AGE: objects younger than this are left for their notify (default 1m)
	reconcileRaw       string
	GoogleClientID     string
	GoogleClientSecret string
	CFAPIToken         string
	CFAccountID        string
	ForwardTo          string
	// Overrides for tests and local fakes; empty means the real Google endpoints.
	GoogleAuthURL   string
	GoogleTokenURL  string
	GoogleRevokeURL string
	GmailAPIBase    string
	CFAPIBase       string
	CFWorkerName    string // Email Worker the catch-all must run (default inbox-worker)
	DeadmanURL      string // pinged after every health snapshot
	// Production is INBOX_ENV=production (set by the container image): serve then refuses every insecure or
	// missing setting instead of warning and disabling the feature.
	Production bool
}

// Load reads config from the given lookup function (os.Getenv in production).
// Optional integrations are left empty and disable their feature; nothing here fails.
func Load(get func(string) string) Config {
	or := func(k, def string) string {
		if v := get(k); v != "" {
			return v
		}
		return def
	}
	c := Config{
		DatabaseURL:        get("DATABASE_URL"),
		HTTPAddr:           or("HTTP_ADDR", ":8080"),
		PublicBaseURL:      get("PUBLIC_BASE_URL"),
		IngestSecret:       get("INGEST_SECRET"),
		S3Endpoint:         get("S3_ENDPOINT"),
		S3Bucket:           get("S3_BUCKET"),
		S3AccessKey:        get("S3_ACCESS_KEY"),
		S3SecretKey:        get("S3_SECRET_KEY"),
		S3Region:           or("S3_REGION", "auto"),
		TokenEncKey:        get("TOKEN_ENC_KEY"),
		TokenEncKeys:       get("TOKEN_ENC_KEYS"),
		OwnerPassword:      get("OWNER_PASSWORD"),
		LoginIPHeader:      get("LOGIN_IP_HEADER"),
		IngestAddr:         or("INGEST_ADDR", ":8081"),
		reconcileRaw:       get("RECONCILE_INTERVAL"),
		GoogleClientID:     get("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: get("GOOGLE_CLIENT_SECRET"),
		CFAPIToken:         get("CF_API_TOKEN"),
		CFAccountID:        get("CF_ACCOUNT_ID"),
		ForwardTo:          get("FORWARD_TO"),
		GoogleAuthURL:      get("GOOGLE_AUTH_URL"),
		GoogleTokenURL:     get("GOOGLE_TOKEN_URL"),
		GoogleRevokeURL:    get("GOOGLE_REVOKE_URL"),
		GmailAPIBase:       get("GMAIL_API_BASE"),
		CFAPIBase:          get("CF_API_BASE"),
		CFWorkerName:       or("CF_WORKER_NAME", "inbox-worker"),
		DeadmanURL:         get("DEADMAN_URL"),
		Production:         strings.EqualFold(strings.TrimSpace(get("INBOX_ENV")), "production"),
	}
	c.ReconcileInterval = 5 * time.Minute
	if d, err := time.ParseDuration(get("RECONCILE_MIN_AGE")); err == nil && d > 0 {
		c.ReconcileMinAge = d
	}
	if d, err := time.ParseDuration(c.reconcileRaw); err == nil && d > 0 {
		c.ReconcileInterval = d
	}
	return c
}

// weakSecrets are placeholder values that must never reach production.
var weakSecrets = map[string]bool{
	"changeme": true, "change-me": true, "password": true, "secret": true, "inbox": true, "postgres": true,
	"dev-ingest-secret": true, "dev-password-inbox-local": true,
}

func weak(v string) bool {
	l := strings.ToLower(strings.TrimSpace(v))
	return weakSecrets[l] || strings.HasPrefix(l, "changeme") || strings.HasPrefix(l, "your-") || strings.HasPrefix(l, "example")
}

// ValidateServe checks the settings `inboxd serve` cannot run without. It reports every problem at once, each
// naming its variable, never a secret value. With INBOX_ENV=production (the image default) it also refuses
// missing integrations, short or placeholder secrets and a non-HTTPS public address.
func (c Config) ValidateServe() error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if c.OwnerPassword == "" {
		bad("OWNER_PASSWORD is not set (the sign-in password, at least %d characters)", MinOwnerPassword)
	} else if utf8.RuneCountInString(c.OwnerPassword) < MinOwnerPassword {
		bad("OWNER_PASSWORD is too short: it must be at least %d characters", MinOwnerPassword)
	} else if c.Production && weak(c.OwnerPassword) {
		bad("OWNER_PASSWORD is a placeholder or well-known value: choose your own")
	}
	if c.reconcileRaw != "" {
		if d, err := time.ParseDuration(c.reconcileRaw); err != nil || d < 10*time.Second {
			bad("RECONCILE_INTERVAL %q must be a duration of at least 10s, for example 5m", c.reconcileRaw)
		}
	}
	if !c.Production {
		return errors.Join(errs...)
	}
	if c.DatabaseURL == "" {
		bad("DATABASE_URL is not set (postgres://user:password@host:5432/db)")
	} else if u, err := url.Parse(c.DatabaseURL); err != nil || u.Host == "" {
		bad("DATABASE_URL is not a valid postgres:// URL")
	} else if pw, _ := u.User.Password(); len(pw) < 16 || weak(pw) {
		bad("DATABASE_URL has a short or placeholder password: use at least 16 random characters (POSTGRES_PASSWORD)")
	}
	if c.PublicBaseURL == "" {
		bad("PUBLIC_BASE_URL is not set (the https:// address of the API, used for the Google OAuth redirect)")
	} else if u, err := url.Parse(c.PublicBaseURL); err != nil || u.Host == "" || u.Scheme != "https" {
		bad("PUBLIC_BASE_URL must be an https:// address (Tailscale Serve provides it), got %q", c.PublicBaseURL)
	} else if strings.HasSuffix(c.PublicBaseURL, "/") {
		bad("PUBLIC_BASE_URL must not end with a slash")
	}
	if c.IngestSecret == "" {
		bad("INGEST_SECRET is not set (shared with the Worker; generate with: openssl rand -hex 32)")
	} else if len(c.IngestSecret) < 32 || weak(c.IngestSecret) {
		bad("INGEST_SECRET is too short or a placeholder: use at least 32 random characters (openssl rand -hex 32)")
	}
	if c.TokenEncKeys == "" && c.TokenEncKey == "" {
		bad("TOKEN_ENC_KEYS is not set (id:base64 keyring that seals the Gmail token; generate with: echo \"k1:$(openssl rand -base64 32)\")")
	} else if _, err := crypto.ParseKeyring(c.TokenEncKeys, c.TokenEncKey); err != nil {
		bad("TOKEN_ENC_KEYS is invalid: %v", err)
	}
	for _, kv := range [][2]string{{"S3_ENDPOINT", c.S3Endpoint}, {"S3_BUCKET", c.S3Bucket}, {"S3_ACCESS_KEY", c.S3AccessKey}, {"S3_SECRET_KEY", c.S3SecretKey}} {
		if kv[1] == "" {
			bad("%s is not set (the Cloudflare R2 bucket the Worker writes raw mail to)", kv[0])
		}
	}
	if c.S3Endpoint != "" {
		if u, err := url.Parse(c.S3Endpoint); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			bad("S3_ENDPOINT must be a URL such as https://<account id>.r2.cloudflarestorage.com")
		}
	}
	if (c.GoogleClientID == "") != (c.GoogleClientSecret == "") {
		bad("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be set together (or both left empty until you connect Gmail)")
	}
	return errors.Join(errs...)
}

func FromEnv() Config { return Load(os.Getenv) }
