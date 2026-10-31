package mail_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/mail"
)

func TestBlobNilReturns503(t *testing.T) {
	r := chi.NewRouter()
	(&mail.API{}).Routes(r)
	id := uuid.NewString()
	for _, c := range [][2]string{
		{"GET", "/messages/" + id + "/body"},
		{"GET", "/messages/" + id + "/raw"},
		{"GET", "/attachments/" + id},
		{"DELETE", "/businesses/" + id},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(c[0], c[1], nil))
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "blob_unavailable") ||
			w.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s %s: %d %s", c[0], c[1], w.Code, w.Body)
		}
	}
}
