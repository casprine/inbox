package telemetry

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLogsURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.example.com":         "https://api.example.com/v1/logs",
		"https://api.example.com/":        "https://api.example.com/v1/logs",
		"https://api.example.com/otlp":    "https://api.example.com/otlp/v1/logs",
		"https://api.example.com/v1/logs": "https://api.example.com/v1/logs",
	} {
		if got, err := LogsURL(in); err != nil || got != want {
			t.Errorf("LogsURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := LogsURL("api.example.com"); err == nil {
		t.Error("want error for a URL without a scheme")
	}
}

func TestValidateProductionNeedsHTTPS(t *testing.T) {
	if (Config{Endpoint: "http://x.test", Production: true}).Validate() == nil {
		t.Error("http endpoint accepted in production")
	}
	if err := (Config{Endpoint: "http://x.test"}).Validate(); err != nil {
		t.Error(err)
	}
	if err := (Config{}).Validate(); err != nil {
		t.Error(err)
	}
}

func TestParseLevel(t *testing.T) {
	if ParseLevel("ERROR") != slog.LevelError || ParseLevel("") != slog.LevelInfo || ParseLevel("nope") != slog.LevelInfo {
		t.Error("level parsing")
	}
}

// Ships a record to a fake collector with the key, honouring the level filter.
func TestSetupShipsAtConfiguredLevel(t *testing.T) {
	got := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- r.URL.Path + "|" + r.Header.Get("Authorization") + "|" + string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	log, shutdown := Setup(context.Background(), Config{Endpoint: srv.URL, IngestKey: "k-123", Level: slog.LevelError})
	log.Info("quiet info line")
	log.Error("loud error line")
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if !strings.HasPrefix(s, "/v1/logs|Bearer k-123|") {
			t.Fatalf("path or key wrong: %.60s", s)
		}
		if !strings.Contains(s, "loud error line") || strings.Contains(s, "quiet info line") {
			t.Fatal("level filter not applied")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing shipped")
	}
}
