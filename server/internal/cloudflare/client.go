// Package cloudflare reads Email Routing state from the Cloudflare API: which zones exist and
// whether each zone's catch-all sends mail to the Inbox worker.
package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/casprine/inbox/server/internal/httpapi"
)

// Routing states, matching the routing_state enum.
const (
	RoutingOK      = "ok"       // catch-all runs the Inbox worker
	RoutingToEmail = "to_email" // catch-all forwards straight to an address
	RoutingNone    = "none"     // email routing off, catch-all disabled or dropping
	RoutingUnknown = "unknown"
)

const DefaultBase = "https://api.cloudflare.com/client/v4"

// Errors callers map to problems.
var (
	ErrNotConfigured = errors.New("cloudflare: not configured")
	ErrUnauthorized  = errors.New("cloudflare: token rejected")
	ErrNotFound      = errors.New("cloudflare: not found")
)

// Client talks to the Cloudflare API with a read-capable token.
type Client struct {
	Token      string
	AccountID  string // optional: limits zones to one account
	WorkerName string // the Email Worker that must receive mail (wrangler name)
	Base       string // default DefaultBase
	HTTP       *http.Client
}

// Configured reports whether a token is present.
func (c *Client) Configured() bool { return c != nil && c.Token != "" }

func (c *Client) base() string {
	if c.Base != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return DefaultBase
}

func (c *Client) worker() string {
	if c.WorkerName != "" {
		return c.WorkerName
	}
	return "inbox-worker"
}

type envelope struct {
	Success bool            `json:"success"`
	Errors  []apiErr        `json:"errors"`
	Result  json.RawMessage `json:"result"`
	Info    struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

type apiErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) get(ctx context.Context, path string, q url.Values) (envelope, error) {
	if !c.Configured() {
		return envelope{}, ErrNotConfigured
	}
	u := c.base() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return envelope{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return envelope{}, httpapi.ScrubURL(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var env envelope
	_ = json.Unmarshal(b, &env)
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return env, ErrUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		return env, ErrNotFound
	case resp.StatusCode/100 != 2 || !env.Success:
		msg := ""
		if len(env.Errors) > 0 {
			msg = env.Errors[0].Message
		}
		return env, fmt.Errorf("cloudflare: status %d %s", resp.StatusCode, msg)
	}
	return env, nil
}

// Zone is a Cloudflare zone (a domain).
type Zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // active | pending | ...
}

// Zones lists all zones visible to the token.
func (c *Client) Zones(ctx context.Context) ([]Zone, error) {
	var out []Zone
	for page := 1; page <= 20; page++ {
		q := url.Values{"per_page": {"50"}, "page": {fmt.Sprint(page)}}
		if c.AccountID != "" {
			q.Set("account.id", c.AccountID)
		}
		env, err := c.get(ctx, "/zones", q)
		if err != nil {
			return nil, err
		}
		var zs []Zone
		if err := json.Unmarshal(env.Result, &zs); err != nil {
			return nil, err
		}
		out = append(out, zs...)
		if env.Info.TotalPages <= page {
			break
		}
	}
	return out, nil
}

// ZoneByName finds the zone whose name is domain.
func (c *Client) ZoneByName(ctx context.Context, domain string) (Zone, error) {
	env, err := c.get(ctx, "/zones", url.Values{"name": {domain}})
	if err != nil {
		return Zone{}, err
	}
	var zs []Zone
	if err := json.Unmarshal(env.Result, &zs); err != nil {
		return Zone{}, err
	}
	for _, z := range zs {
		if strings.EqualFold(z.Name, domain) {
			return z, nil
		}
	}
	return Zone{}, ErrNotFound
}

// RoutingState decides where a zone's catch-all sends mail.
func (c *Client) RoutingState(ctx context.Context, zoneID string) (string, error) {
	env, err := c.get(ctx, "/zones/"+url.PathEscape(zoneID)+"/email/routing", nil)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return RoutingUnknown, err
	}
	if err == nil {
		var s struct {
			Enabled bool `json:"enabled"`
		}
		if json.Unmarshal(env.Result, &s) == nil && !s.Enabled {
			return RoutingNone, nil
		}
	}
	env, err = c.get(ctx, "/zones/"+url.PathEscape(zoneID)+"/email/routing/rules/catch_all", nil)
	if errors.Is(err, ErrNotFound) {
		return RoutingNone, nil
	}
	if err != nil {
		return RoutingUnknown, err
	}
	var r struct {
		Enabled bool `json:"enabled"`
		Actions []struct {
			Type  string   `json:"type"`
			Value []string `json:"value"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(env.Result, &r); err != nil {
		return RoutingUnknown, err
	}
	if !r.Enabled || len(r.Actions) == 0 {
		return RoutingNone, nil
	}
	switch a := r.Actions[0]; a.Type {
	case "worker":
		for _, v := range a.Value {
			if v == c.worker() {
				return RoutingOK, nil
			}
		}
		return RoutingNone, nil // another worker: mail does not reach Inbox
	case "forward":
		return RoutingToEmail, nil
	}
	return RoutingNone, nil // drop and anything else
}
