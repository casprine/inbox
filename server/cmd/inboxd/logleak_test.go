package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/config"
	"github.com/casprine/inbox/server/internal/ingest"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// Runs the real serve() with distinctive secrets, drives sign-in (right and wrong), device-token use, a bad and a good
// signed notify, a mail with a recognisable body and a failing Gmail connect, then shuts it down with the context (the
// SIGTERM path) and asserts that none of the secrets reached the logs. It also proves graceful shutdown returns nil.
func TestServeLogsNeverContainSecrets(t *testing.T) {
	base, ep := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_S3_ENDPOINT")
	if base == "" || ep == "" {
		t.Skip("TEST_DATABASE_URL / TEST_S3_ENDPOINT not set")
	}
	ctx := context.Background()
	schema := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	const (
		ownerPW     = "OWNERPW-MARKER-correct-horse"
		wrongPW     = "WRONGPW-MARKER-guess-guess"
		ingestSec   = "INGESTSECRET-MARKER-0123456789abcdef"
		googleSec   = "GOOGLESECRET-MARKER-xyz"
		bodyMarker  = "RAWBODY-MARKER-private-words"
		badTokenVal = "BADTOKEN-MARKER-not-a-device-token"
	)
	encKey := base64.StdEncoding.EncodeToString([]byte("ENCKEY-MARKER-0123456789abcdefgh"[:32]))
	api, ing := freeAddr(t), freeAddr(t)
	env := map[string]string{
		"DATABASE_URL": base + sep + "search_path=" + schema + ",public", "HTTP_ADDR": api, "INGEST_ADDR": ing,
		"PUBLIC_BASE_URL": "http://" + api, "OWNER_PASSWORD": ownerPW, "INGEST_SECRET": ingestSec,
		"S3_ENDPOINT": ep, "S3_BUCKET": "inbox-mail", "S3_ACCESS_KEY": "minio", "S3_SECRET_KEY": "minio12345",
		"TOKEN_ENC_KEYS": "k1:" + encKey, "GOOGLE_CLIENT_ID": "cid", "GOOGLE_CLIENT_SECRET": googleSec,
		"GOOGLE_TOKEN_URL": "http://127.0.0.1:1/token", "RECONCILE_INTERVAL": "10s", "RECONCILE_MIN_AGE": "1s",
	}
	cfg := config.Load(func(k string) string { return env[k] })
	logs := &syncBuf{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sctx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- serve(sctx, log, cfg) }()

	call := func(method, url, token string, body string, hdr map[string]string) (int, string) {
		req, _ := http.NewRequest(method, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if st, _ := call("GET", "http://"+api+"/readyz", "", "", nil); st == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server never became ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if st, b := call("GET", "http://"+api+"/healthz", "", "", nil); st != 200 {
		t.Fatalf("healthz %d %s", st, b)
	}
	// sign in: wrong then right
	if st, _ := call("POST", "http://"+api+"/v1/auth/login", "", `{"password":"`+wrongPW+`"}`, nil); st != 401 {
		t.Fatalf("wrong password: %d", st)
	}
	st, b := call("POST", "http://"+api+"/v1/auth/login", "", `{"password":"`+ownerPW+`","device_name":"t"}`, nil)
	if st != 200 {
		t.Fatalf("login: %d %s", st, b)
	}
	token := b[strings.Index(b, `"token":"`)+9:]
	token = token[:strings.Index(token, `"`)]
	if st, _ := call("GET", "http://"+api+"/v1/businesses", badTokenVal, "", nil); st != 401 {
		t.Fatalf("bad token: %d", st)
	}
	if st, b := call("POST", "http://"+api+"/v1/businesses", token, `{"name":"Acme","domain":"mydomain.com"}`, nil); st != 201 && st != 200 {
		t.Fatalf("create business: %d %s", st, b)
	}
	// Gmail connect succeeds (builds the URL), the callback's token exchange fails against a closed port.
	st, b = call("POST", "http://"+api+"/v1/gmail/connect", token, "", nil)
	if st != 200 {
		t.Fatalf("gmail connect: %d %s", st, b)
	}
	state := b[strings.Index(b, "state=")+6:]
	state = state[:strings.IndexAny(state, `&"`)]
	call("GET", "http://"+api+"/oauth/google/callback?code=AUTHCODE-MARKER&state="+state, "", "", nil)

	// a mail with a recognisable body, announced with a good signature; and a bad one
	bl := blob.New(blob.Config{Endpoint: ep, Bucket: "inbox-mail", AccessKey: "minio", SecretKey: "minio12345"})
	raw := "From: s@example.org\r\nTo: hello@mydomain.com\r\nSubject: logleak\r\nMessage-ID: <" + uuid.NewString() + "@x>\r\nDate: Mon, 01 Jan 2026 10:00:00 +0000\r\n\r\n" + bodyMarker + "\r\n"
	now := time.Now().UTC()
	key := fmt.Sprintf("raw/%d/%02d/test-%s.eml", now.Year(), int(now.Month()), uuid.NewString())
	if err := bl.Put(ctx, key, strings.NewReader(raw), int64(len(raw)), map[string]string{"env_from": "s@example.org", "env_to": "hello@mydomain.com"}); err != nil {
		t.Fatal(err)
	}
	defer bl.Delete(ctx, key)
	payload := `{"key":"` + key + `","env_from":"s@example.org","env_to":"hello@mydomain.com","raw_size":` + strconv.Itoa(len(raw)) + `,"received_at":"` + now.Format(time.RFC3339) + `","message_id":"m"}`
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	hdr := func(sig string) map[string]string {
		return map[string]string{"X-Inbox-Timestamp": ts, "X-Inbox-Signature": sig}
	}
	if st, _ := call("POST", "http://"+ing+"/ingest/v1/notify", "", payload, hdr("BADSIG-MARKER")); st != 401 {
		t.Fatalf("bad signature: %d", st)
	}
	good := ingest.Sign(ingestSec, ts, []byte(payload))
	if st, b := call("POST", "http://"+ing+"/ingest/v1/notify", "", payload, hdr(good)); st != 202 {
		t.Fatalf("notify: %d %s", st, b)
	}
	for end := time.Now().Add(30 * time.Second); ; time.Sleep(300 * time.Millisecond) {
		if _, b := call("GET", "http://"+api+"/v1/messages", token, "", nil); strings.Contains(b, "logleak") {
			break
		}
		if time.Now().After(end) {
			t.Fatal("message never appeared")
		}
	}
	if st, b := call("GET", "http://"+api+"/readyz", "", "", nil); st != 200 {
		t.Fatalf("readyz: %d %s", st, b)
	}

	stop() // what SIGTERM does
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful shutdown returned %v", err)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("serve did not return after shutdown")
	}
	out := logs.String()
	if !strings.Contains(out, `"listening"`) || !strings.Contains(out, "stopped") {
		t.Fatalf("expected startup and shutdown lines in the log:\n%s", out)
	}
	for _, secret := range []string{ownerPW, wrongPW, ingestSec, googleSec, bodyMarker, badTokenVal, token, good, "BADSIG-MARKER", "AUTHCODE-MARKER", encKey, "ENCKEY-MARKER", "minio12345"} {
		if strings.Contains(out, secret) {
			t.Errorf("log contains %q", secret)
		}
	}
}
