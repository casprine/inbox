// Package telemetry ships the server's logs to an OpenTelemetry (OTLP/HTTP) backend. It is off unless an endpoint is
// configured; stdout JSON logging is always on and unaffected.
package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

// Config is read from OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_INGEST_KEY, OTEL_LOG_LEVEL and OTEL_EXPORTER_OTLP_HEADERS.
type Config struct {
	Endpoint   string     // base URL of the collector/vendor; /v1/logs is appended when missing
	IngestKey  string     // sent as "Authorization: Bearer <key>"
	Level      slog.Level // minimum level shipped (stdout is unaffected)
	Headers    map[string]string
	Production bool
}

// FromEnv reads the telemetry settings. A bad level falls back to info.
func FromEnv(get func(string) string, production bool) Config {
	c := Config{
		Endpoint:   strings.TrimSpace(get("OTEL_EXPORTER_OTLP_ENDPOINT")),
		IngestKey:  strings.TrimSpace(get("OTEL_INGEST_KEY")),
		Level:      ParseLevel(get("OTEL_LOG_LEVEL")),
		Headers:    map[string]string{},
		Production: production,
	}
	for _, kv := range strings.Split(get("OTEL_EXPORTER_OTLP_HEADERS"), ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.TrimSpace(k) != "" {
			c.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return c
}

// ParseLevel maps error|warn|info|debug (case-insensitive) to a slog level; anything else is info.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "error":
		return slog.LevelError
	case "warn", "warning":
		return slog.LevelWarn
	case "debug":
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// LogsURL turns the configured endpoint into the OTLP logs URL.
func LogsURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must be an http(s) URL")
	}
	if !strings.HasSuffix(u.Path, "/v1/logs") {
		u.Path = strings.TrimRight(u.Path, "/") + "/v1/logs"
	}
	return u.String(), nil
}

// Validate is the production check: telemetry carries log lines, so it must go over https.
func (c Config) Validate() error {
	if c.Endpoint == "" {
		return nil
	}
	u, err := LogsURL(c.Endpoint)
	if err != nil {
		return err
	}
	if c.Production && !strings.HasPrefix(u, "https://") {
		return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must be https in production")
	}
	return nil
}

type levelHandler struct {
	slog.Handler
	min slog.Level
}

func (h levelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}
func (h levelHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return levelHandler{h.Handler.WithAttrs(a), h.min}
}
func (h levelHandler) WithGroup(n string) slog.Handler {
	return levelHandler{h.Handler.WithGroup(n), h.min}
}

// Setup returns the process logger (stdout JSON, plus OTLP when configured) and a shutdown that flushes pending
// records. A misconfigured endpoint is reported on stdout and telemetry is skipped; it never stops the server.
func Setup(ctx context.Context, c Config) (*slog.Logger, func(context.Context) error) {
	stdout := slog.NewJSONHandler(os.Stdout, nil)
	noop := func(context.Context) error { return nil }
	if c.Endpoint == "" {
		return slog.New(stdout), noop
	}
	fail := func(err error) (*slog.Logger, func(context.Context) error) {
		l := slog.New(stdout)
		l.Error("telemetry disabled", "err", err.Error())
		return l, noop
	}
	target, err := LogsURL(c.Endpoint)
	if err != nil {
		return fail(err)
	}
	headers := map[string]string{}
	for k, v := range c.Headers {
		headers[k] = v
	}
	if c.IngestKey != "" {
		headers["Authorization"] = "Bearer " + c.IngestKey
	}
	opts := []otlploghttp.Option{otlploghttp.WithEndpointURL(target), otlploghttp.WithTimeout(10 * time.Second)}
	if len(headers) > 0 {
		opts = append(opts, otlploghttp.WithHeaders(headers))
	}
	exp, err := otlploghttp.New(ctx, opts...)
	if err != nil {
		return fail(err)
	}
	env := "development"
	if c.Production {
		env = "production"
	}
	res := resource.NewSchemaless(attribute.String("service.name", "inbox"), attribute.String("deployment.environment", env))
	prov := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)))
	otel := levelHandler{otelslog.NewHandler("inbox", otelslog.WithLoggerProvider(prov)), c.Level}
	return slog.New(slog.NewMultiHandler(stdout, otel)), prov.Shutdown
}
