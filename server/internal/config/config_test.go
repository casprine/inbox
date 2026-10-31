package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	c := Load(func(k string) string { return map[string]string{"DATABASE_URL": "x"}[k] })
	if c.HTTPAddr != ":8080" || c.S3Region != "auto" || c.DatabaseURL != "x" {
		t.Fatalf("unexpected: %+v", c)
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestIngestAndReconcileDefaults(t *testing.T) {
	c := Load(env(nil))
	if c.IngestAddr != ":8081" || c.ReconcileInterval != 5*time.Minute {
		t.Fatalf("defaults: %+v", c)
	}
	c = Load(env(map[string]string{"INGEST_ADDR": "127.0.0.1:9", "RECONCILE_INTERVAL": "90s"}))
	if c.IngestAddr != "127.0.0.1:9" || c.ReconcileInterval != 90*time.Second {
		t.Fatalf("overrides: %+v", c)
	}
}

func TestValidateServe(t *testing.T) {
	good := map[string]string{"OWNER_PASSWORD": "twelve chars"}
	cases := []struct {
		name string
		env  map[string]string
		ok   bool
	}{
		{"missing password", nil, false},
		{"short password", map[string]string{"OWNER_PASSWORD": "elevenchars"}, false},
		{"12 characters", good, true},
		{"multibyte counts characters", map[string]string{"OWNER_PASSWORD": "пароль-пароль"}, true},
		{"bad interval", map[string]string{"OWNER_PASSWORD": "twelve chars", "RECONCILE_INTERVAL": "soon"}, false},
		{"interval too small", map[string]string{"OWNER_PASSWORD": "twelve chars", "RECONCILE_INTERVAL": "1s"}, false},
		{"good interval", map[string]string{"OWNER_PASSWORD": "twelve chars", "RECONCILE_INTERVAL": "10m"}, true},
	}
	for _, c := range cases {
		if err := Load(env(c.env)).ValidateServe(); (err == nil) != c.ok {
			t.Errorf("%s: err=%v", c.name, err)
		}
	}
}

func TestProductionNamesEveryProblemAndNoSecret(t *testing.T) {
	err := Load(env(map[string]string{"INBOX_ENV": "production", "OWNER_PASSWORD": "short", "INGEST_SECRET": "tooshortsecret-VALUE",
		"PUBLIC_BASE_URL": "http://mini.local", "DATABASE_URL": "postgres://inbox:inbox@db:5432/inbox", "TOKEN_ENC_KEYS": "k1:not-base64-SECRETKEY"})).ValidateServe()
	if err == nil {
		t.Fatal("expected errors")
	}
	msg := err.Error()
	for _, name := range []string{"OWNER_PASSWORD", "INGEST_SECRET", "PUBLIC_BASE_URL", "DATABASE_URL", "TOKEN_ENC_KEYS", "S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY"} {
		if !strings.Contains(msg, name) {
			t.Errorf("error does not name %s:\n%s", name, msg)
		}
	}
	for _, leak := range []string{"tooshortsecret-VALUE", "SECRETKEY"} {
		if strings.Contains(msg, leak) {
			t.Errorf("error leaks %q", leak)
		}
	}
}

func TestProductionAcceptsAGoodConfigAndRefusesPlaceholders(t *testing.T) {
	good := map[string]string{"INBOX_ENV": "production", "OWNER_PASSWORD": "a long unique password", "INGEST_SECRET": strings.Repeat("ab", 32),
		"PUBLIC_BASE_URL": "https://mini.tail1.ts.net", "DATABASE_URL": "postgres://inbox:" + strings.Repeat("x9", 12) + "@postgres:5432/inbox?sslmode=disable",
		"TOKEN_ENC_KEYS": "k1:" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		"S3_ENDPOINT":    "https://acct.r2.cloudflarestorage.com", "S3_BUCKET": "inbox-mail", "S3_ACCESS_KEY": "a", "S3_SECRET_KEY": "b"}
	if err := Load(env(good)).ValidateServe(); err != nil {
		t.Fatalf("good production config refused: %v", err)
	}
	for k, v := range map[string]string{"OWNER_PASSWORD": "changeme-changeme", "INGEST_SECRET": "dev-ingest-secret", "PUBLIC_BASE_URL": "http://x.ts.net", "GOOGLE_CLIENT_ID": "only-id"} {
		m := map[string]string{}
		for a, b := range good {
			m[a] = b
		}
		m[k] = v
		if Load(env(m)).ValidateServe() == nil {
			t.Errorf("%s=%q should be refused", k, v)
		}
	}
	// development stays lenient
	if err := Load(env(map[string]string{"OWNER_PASSWORD": "twelve chars"})).ValidateServe(); err != nil {
		t.Fatalf("dev: %v", err)
	}
}
