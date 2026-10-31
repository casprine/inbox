package mail

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeRefresher struct {
	calls int
	err   error
}

func (f *fakeRefresher) RequestReconcile(context.Context) error { f.calls++; return f.err }

func TestRefreshQueuesCatchUp(t *testing.T) {
	f := &fakeRefresher{}
	a := &API{Refresh: f}
	w := httptest.NewRecorder()
	a.refresh(w, httptest.NewRequest("POST", "/v1/mail:refresh", nil))
	if w.Code != http.StatusAccepted || f.calls != 1 || !strings.Contains(w.Body.String(), `"queued"`) {
		t.Fatalf("%d %s calls=%d", w.Code, w.Body, f.calls)
	}
}

func TestRefreshWithoutQueueOrOnFailure(t *testing.T) {
	w := httptest.NewRecorder()
	(&API{}).refresh(w, httptest.NewRequest("POST", "/v1/mail:refresh", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "queue_unavailable") {
		t.Fatalf("no queue: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	(&API{Refresh: &fakeRefresher{err: errors.New("db down")}}).refresh(w, httptest.NewRequest("POST", "/v1/mail:refresh", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "enqueue_failed") {
		t.Fatalf("failure: %d %s", w.Code, w.Body)
	}
}
