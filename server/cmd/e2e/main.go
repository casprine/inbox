// Command e2e drives a real inboxd process end to end: it plays the Cloudflare Worker (signed
// notifications plus raw mail in object storage), Google (OAuth + Gmail API) and Cloudflare's API
// with local fakes, then checks what the iPhone app would see through the HTTP API.
//
// Run it with scripts/e2e.sh, which starts the docker-compose services and builds inboxd.
// It talks to nothing outside this machine and uses a throw-away database.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/cloudflare/cftest"
	"github.com/casprine/inbox/server/internal/gmail/gmailtest"
	"github.com/casprine/inbox/server/internal/ingest/parse"
)

const (
	domain        = "mydomain.com"
	ingestSecret  = "e2e-ingest-secret"
	ownerPassword = "e2e owner password 123"
)

type driver struct {
	base       string
	ingestBase string // the second listener (only /ingest/v1/notify)
	token      string
	pool       *pgxpool.Pool
	blob       *blob.Store
	gfake      *gmailtest.Fake
	cffake     *cftest.Fake
	http       *http.Client

	logMu   sync.Mutex
	logs    []string
	sseMu   sync.Mutex
	sse     []map[string]any
	passed  int
	bizID   string
	samples map[string]sample // fixture name -> what we pushed

	workerURL string // the real Worker harness (empty: Go simulation of the Worker)
}

type sample struct {
	raw       []byte
	messageID string
	envTo     string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nE2E FAILED: %v\n", err)
		os.Exit(1)
	}
}

func env(k string) string {
	v := os.Getenv(k)
	if v == "" {
		fmt.Fprintf(os.Stderr, "missing %s (run scripts/e2e.sh)\n", k)
		os.Exit(2)
	}
	return v
}

func run() error {
	ctx := context.Background()
	adminURL, s3 := env("E2E_DB_ADMIN_URL"), env("E2E_S3_ENDPOINT")
	inboxd := env("E2E_INBOXD")

	// Throw-away database.
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		return err
	}
	defer admin.Close()
	dbName := "e2e_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		return err
	}
	defer func() { _, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)") }()
	dbURL := replaceDB(adminURL, dbName)

	d := &driver{http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		samples: map[string]sample{}}
	d.blob = blob.New(blob.Config{Endpoint: s3, Bucket: "inbox-mail", AccessKey: "minio", SecretKey: "minio12345"})
	// The server ingests every raw/ object its database lacks, and the bucket is shared with the Go tests,
	// which leave objects behind: start from an empty raw/ (the scenario ends by checking it is empty again).
	if old, err := d.blob.List(ctx, "raw/", ""); err == nil && len(old) > 0 {
		keys := make([]string, len(old))
		for i, o := range old {
			keys[i] = o.Key
		}
		if err := d.blob.DeleteMany(ctx, keys); err != nil {
			return err
		}
	}
	d.gfake = gmailtest.New()
	defer d.gfake.Close()
	d.cffake = cftest.New("cf-token")
	defer d.cffake.Close()
	d.cffake.AddZone(cftest.Zone{ID: "zone-1", Name: domain, RoutingEnabled: true, CatchAllType: "worker", CatchAllValue: "inbox-worker", CatchAllOn: true})

	port, err := freePort()
	if err != nil {
		return err
	}
	ingestPort, err := freePort()
	if err != nil {
		return err
	}
	d.base = fmt.Sprintf("http://127.0.0.1:%d", port)
	d.ingestBase = fmt.Sprintf("http://127.0.0.1:%d", ingestPort)
	encKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	childEnv := append(os.Environ(),
		"DATABASE_URL="+dbURL, "HTTP_ADDR=127.0.0.1:"+strconv.Itoa(port), "PUBLIC_BASE_URL="+d.base,
		"INGEST_SECRET="+ingestSecret, "S3_ENDPOINT="+s3, "S3_BUCKET=inbox-mail", "S3_ACCESS_KEY=minio", "S3_SECRET_KEY=minio12345", "S3_REGION=auto",
		"TOKEN_ENC_KEY="+encKey, "OWNER_PASSWORD="+ownerPassword, "INGEST_ADDR=127.0.0.1:"+strconv.Itoa(ingestPort),
		"LOGIN_IP_HEADER=X-E2E-Client", "RECONCILE_MIN_AGE=1s",
		"GOOGLE_CLIENT_ID=e2e-client", "GOOGLE_CLIENT_SECRET=e2e-secret", "GOOGLE_AUTH_URL="+d.gfake.AuthURL(), "GOOGLE_TOKEN_URL="+d.gfake.TokenURL(),
		"GOOGLE_REVOKE_URL="+d.gfake.RevokeURL(), "GMAIL_API_BASE="+d.gfake.APIBase(),
		"CF_API_TOKEN=cf-token", "CF_API_BASE="+d.cffake.URL, "CF_WORKER_NAME=inbox-worker")

	// migrate, then serve
	mig := exec.Command(inboxd, "migrate")
	mig.Env = childEnv
	if out, err := mig.CombinedOutput(); err != nil {
		return fmt.Errorf("migrate: %v\n%s", err, out)
	}
	d.step("migrations apply to an empty database")

	srv := exec.Command(inboxd, "serve")
	srv.Env = childEnv
	stdout, _ := srv.StdoutPipe()
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		return err
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			d.logMu.Lock()
			d.logs = append(d.logs, sc.Text())
			d.logMu.Unlock()
		}
	}()
	exited := make(chan error, 1)
	go func() { exited <- srv.Wait() }()
	defer func() {
		if srv.ProcessState == nil {
			_ = srv.Process.Kill()
		}
	}()

	if err := d.waitFor("server answers /readyz", 20*time.Second, func() bool {
		s, _, _ := d.get("/readyz", false)
		return s == 200
	}); err != nil {
		return err
	}

	if d.pool, err = pgxpool.New(ctx, dbURL); err != nil {
		return err
	}
	defer d.pool.Close()
	// The app's first step: sign in with the owner password and keep the device token.
	if s, b, _ := d.do("POST", "/v1/auth/login", J{"password": "definitely not the password"}, false, nil); s != http.StatusUnauthorized {
		return fmt.Errorf("login with a wrong password answered %d %s", s, b)
	}
	d.step("login with a wrong password is refused (401)")
	s, b, _ := d.do("POST", "/v1/auth/login", J{"password": ownerPassword, "device_name": "e2e"}, false, nil)
	var lr struct {
		DeviceID string `json:"device_id"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(b, &lr); err != nil || s != http.StatusOK || lr.Token == "" {
		return fmt.Errorf("login with the owner password answered %d %s", s, b)
	}
	d.token = lr.Token
	d.step("login with the owner password returns a device token")

	if os.Getenv("E2E_WORKER") != "sim" {
		stop, err := d.startWorker(inboxdDir(inboxd))
		if err != nil {
			return fmt.Errorf("starting the real Worker: %w (set E2E_WORKER=sim to use the Go simulation)", err)
		}
		defer stop()
		d.step("the real Worker (edge/src/index.ts) runs in Node against the local server")
	} else {
		fmt.Println("  --  E2E_WORKER=sim: using the Go simulation of the Worker, not edge/src")
	}

	if err := d.scenario(ctx); err != nil {
		return err
	}

	// Clean shutdown.
	_ = srv.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-exited:
		if err != nil {
			return fmt.Errorf("server exited uncleanly after SIGTERM: %v", err)
		}
	case <-time.After(20 * time.Second):
		return errors.New("server did not stop within 20s of SIGTERM")
	}
	d.step("server shuts down cleanly on SIGTERM")

	// The server must not have logged any error while all this happened.
	d.logMu.Lock()
	defer d.logMu.Unlock()
	for _, l := range d.logs {
		if strings.Contains(l, `"level":"ERROR"`) {
			return fmt.Errorf("server logged an error: %s", l)
		}
	}
	d.step("server logged no errors")
	fmt.Printf("\nE2E PASSED: %d checks\n", d.passed)
	return nil
}

// inboxdDir is server/bin; the repository root is two levels up.
func inboxdDir(inboxd string) string { return filepath.Dir(filepath.Dir(filepath.Dir(inboxd))) }

func replaceDB(u, name string) string {
	p, _ := url.Parse(u)
	p.Path = "/" + name
	return p.String()
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (d *driver) step(name string) {
	d.passed++
	fmt.Printf("  ok  %s\n", name)
}

func (d *driver) waitFor(name string, timeout time.Duration, f func() bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f() {
			d.step(name)
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %v: %s", timeout, name)
}

// ---- HTTP helpers ----

func (d *driver) do(method, path string, body any, auth bool, hdr map[string]string) (int, []byte, http.Header) {
	return d.doAt(d.base, method, path, body, auth, hdr)
}

func (d *driver) doAt(base, method, path string, body any, auth bool, hdr map[string]string) (int, []byte, http.Header) {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	if auth {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}
	if method != http.MethodGet && auth {
		req.Header.Set("Idempotency-Key", uuid.NewString())
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return 0, []byte(err.Error()), nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}

func (d *driver) get(path string, auth bool) (int, []byte, http.Header) {
	return d.do("GET", path, nil, auth, nil)
}

type J = map[string]any

// api calls the authenticated API and decodes a JSON object (nil for empty bodies).
func (d *driver) api(method, path string, body any) (int, J) {
	s, b, _ := d.do(method, path, body, true, nil)
	var m J
	_ = json.Unmarshal(b, &m)
	return s, m
}

func (d *driver) must(method, path string, body any, want int) J {
	s, m := d.api(method, path, body)
	if s != want {
		panic(fmt.Errorf("%s %s: HTTP %d, want %d: %v", method, path, s, want, m))
	}
	return m
}

// at walks nested maps/slices: at(m, "messages", 0, "id").
func at(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			a, ok := v.([]any)
			if !ok || k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func (d *driver) check(cond bool, format string, a ...any) {
	if !cond {
		panic(fmt.Errorf(format, a...))
	}
}

// ---- the simulated Worker ----

func sign(ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(ingestSecret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// workerDelivers does what the Email Worker does for one mail: raw message into R2, then a signed notification.
func (d *driver) workerDelivers(ctx context.Context, name string, raw []byte, envTo string, storeOK bool) error {
	p, err := parse.Parse(raw)
	if err != nil {
		return err
	}
	if d.workerURL != "" {
		return d.realWorkerDelivers(name, raw, envTo, storeOK, p.MessageID)
	}
	now := time.Now().UTC()
	payload := J{"env_from": "sender@example.org", "env_to": envTo, "raw_size": len(raw), "received_at": now.Format(time.RFC3339),
		"message_id": "<" + p.MessageID + ">"}
	if storeOK {
		key := fmt.Sprintf("raw/%04d/%02d/%s-%s.eml", now.Year(), now.Month(), name, uuid.NewString()[:8])
		if err := d.blob.Put(ctx, key, bytes.NewReader(raw), int64(len(raw)), nil); err != nil {
			return err
		}
		payload["key"] = key
	} else {
		payload["key"], payload["reason"] = nil, "store_failed"
	}
	body, _ := json.Marshal(payload)
	ts := strconv.FormatInt(now.Unix(), 10)
	s, b, _ := d.doAt(d.ingestBase, "POST", "/ingest/v1/notify", body, false, map[string]string{
		"Content-Type": "application/json", "X-Inbox-Timestamp": ts, "X-Inbox-Signature": sign(ts, body)})
	if s != http.StatusAccepted {
		return fmt.Errorf("notify %s: HTTP %d %s", name, s, b)
	}
	d.samples[name] = sample{raw, p.MessageID, envTo}
	return nil
}

func (d *driver) fixture(name string) []byte {
	b, err := parse.Fixtures.ReadFile("testdata/" + name + ".eml")
	if err != nil {
		panic(err)
	}
	return b
}

func (d *driver) count(sql string, args ...any) int {
	var n int
	if err := d.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		panic(err)
	}
	return n
}

func (d *driver) messages() []any {
	_, m := d.api("GET", "/v1/messages?limit=200", nil)
	l, _ := m["messages"].([]any)
	return l
}

func (d *driver) messageBySubject(subject string) (id string, ok bool) {
	for _, m := range d.messages() {
		if at(m, "subject") == subject {
			return at(m, "id").(string), true
		}
	}
	return "", false
}

func mailEML(msgID, from, to, subject, body string, date time.Time) []byte {
	return []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nMessage-ID: <%s>\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		from, to, msgID, subject, date.Format(time.RFC1123Z), body))
}
