package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Problem is an RFC 9457 problem document with a stable machine-readable code.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// WriteProblem writes an application/problem+json response.
func WriteProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{
		Type: "about:blank", Title: http.StatusText(status), Status: status, Code: code, Detail: detail,
	})
}

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) { writeJSON(w, status, v) }

// WriteProblemExt is WriteProblem plus extra top-level members (RFC 9457 extensions).
func WriteProblemExt(w http.ResponseWriter, status int, code, detail string, ext map[string]any) {
	body := map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code}
	if detail != "" {
		body["detail"] = detail
	}
	for k, v := range ext {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ScrubURL strips the request URL from a transport error. Outgoing URLs can carry a Message-ID, a
// search query or a secret (health ping keys), and *url.Error prints them, so errors that may be
// logged go through this first. The wrapped cause (timeouts, context errors) is kept.
func ScrubURL(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	host := ""
	if u, perr := url.Parse(ue.URL); perr == nil {
		host = u.Host
	}
	return fmt.Errorf("%s %s: %w", ue.Op, host, ue.Err)
}
