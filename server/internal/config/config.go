// Package config parses environment configuration.
package config

import (
	"fmt"
	"os"
	"time"
	"unicode/utf8"
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

// ValidateServe checks the settings `inboxd serve` cannot run without: the owner password and a
// well-formed RECONCILE_INTERVAL.
func (c Config) ValidateServe() error {
	if c.OwnerPassword == "" {
		return fmt.Errorf("OWNER_PASSWORD is not set (the sign-in password, at least %d characters)", MinOwnerPassword)
	}
	if utf8.RuneCountInString(c.OwnerPassword) < MinOwnerPassword {
		return fmt.Errorf("OWNER_PASSWORD is too short: it must be at least %d characters", MinOwnerPassword)
	}
	if c.reconcileRaw != "" {
		if d, err := time.ParseDuration(c.reconcileRaw); err != nil || d < 10*time.Second {
			return fmt.Errorf("RECONCILE_INTERVAL %q must be a duration of at least 10s, for example 5m", c.reconcileRaw)
		}
	}
	return nil
}

func FromEnv() Config { return Load(os.Getenv) }
