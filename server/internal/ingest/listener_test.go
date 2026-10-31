package ingest_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/casprine/inbox/server/internal/ingest"
)

type countingEnqueuer struct{ n int }

func (c *countingEnqueuer) EnqueueIngest(*http.Request, ingest.IngestArgs) error { c.n++; return nil }

// The dedicated ingest listener is published on its own (Tailscale Funnel): it must serve the signed
// notify and nothing else.
func TestListenerHandlerServesOnlyNotify(t *testing.T) {
	enq := &countingEnqueuer{}
	srv := httptest.NewServer(ingest.ListenerHandler("s3cret", enq))
	defer srv.Close()

	body := []byte(`{"key":"raw/2026/10/01ABC.eml","env_from":"a@b.c","env_to":"x@y.z"}`)
	post := func(path string, sign bool, ts time.Time) int {
		req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(body))
		t := strconv.FormatInt(ts.Unix(), 10)
		req.Header.Set("X-Inbox-Timestamp", t)
		if sign {
			req.Header.Set("X-Inbox-Signature", ingest.Sign("s3cret", t, body))
		} else {
			req.Header.Set("X-Inbox-Signature", "00")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := post("/ingest/v1/notify", true, time.Now()); got != 202 {
		t.Fatalf("signed notify: %d", got)
	}
	if got := post("/ingest/v1/notify", false, time.Now()); got != 401 {
		t.Fatalf("bad signature: %d", got)
	}
	if got := post("/ingest/v1/notify", true, time.Now().Add(-time.Hour)); got != 401 {
		t.Fatalf("stale timestamp: %d", got)
	}
	if enq.n != 1 {
		t.Fatalf("enqueued %d, want 1", enq.n)
	}
	for _, path := range []string{"/v1/businesses", "/v1/auth/login", "/healthz", "/readyz", "/v1/events", "/oauth/google/callback", "/"} {
		for _, method := range []string{"GET", "POST"} {
			req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(body))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 404 {
				t.Errorf("%s %s: %d, want 404", method, path, resp.StatusCode)
			}
		}
	}
}
