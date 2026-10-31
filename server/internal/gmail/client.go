package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/casprine/inbox/server/internal/httpapi"
)

// APIError is a non-2xx answer from the Gmail API.
type APIError struct {
	Status     int
	Reason     string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("gmail api %d %s: %s", e.Status, e.Reason, e.Message)
}

// RateLimited reports 429 and the 403 quota reasons Google uses for rate limiting.
func (e *APIError) RateLimited() bool {
	if e.Status == http.StatusTooManyRequests {
		return true
	}
	return e.Status == http.StatusForbidden && (e.Reason == "rateLimitExceeded" || e.Reason == "userRateLimitExceeded")
}

// limiter is the global Gmail request limiter (15 req/s).
func (s *Service) limiter() *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lim == nil {
		s.lim = rate.NewLimiter(15, 15)
	}
	return s.lim
}

// call performs an authorised, rate-limited Gmail API request and decodes the JSON answer into out
// (nil to ignore). A 401 drops the cached access token and retries once.
func (s *Service) call(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		tok, err := s.AccessToken(ctx)
		if err != nil {
			return err
		}
		if err := s.limiter().Wait(ctx); err != nil {
			return err
		}
		u := s.Cfg.apiBase() + path
		if len(query) > 0 {
			u += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := s.http().Do(req)
		if err != nil {
			return httpapi.ScrubURL(err)
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if resp.StatusCode/100 == 2 {
			if out != nil && len(b) > 0 {
				return json.Unmarshal(b, out)
			}
			return nil
		}
		ae := parseAPIError(resp, b)
		if ae.Status == http.StatusUnauthorized && attempt == 0 {
			s.dropAccessToken(ctx)
			continue
		}
		return ae
	}
}

func parseAPIError(resp *http.Response, body []byte) *APIError {
	ae := &APIError{Status: resp.StatusCode}
	var e struct {
		Error struct {
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil {
		ae.Message = e.Error.Message
		if len(e.Error.Errors) > 0 {
			ae.Reason = e.Error.Errors[0].Reason
		}
	}
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			ae.RetryAfter = time.Duration(secs) * time.Second
		} else if t, err := http.ParseTime(v); err == nil {
			ae.RetryAfter = time.Until(t)
		}
	}
	return ae
}

func (s *Service) dropAccessToken(ctx context.Context) {
	if a, err := s.Store.GetGmailAccount(ctx); err == nil {
		s.mu.Lock()
		delete(s.access, a.ID.String())
		s.mu.Unlock()
	}
}

// FindByRFC822 looks a message up by its Message-ID header. found=false when Gmail has no such message.
func (s *Service) FindByRFC822(ctx context.Context, messageID string) (gmailID, threadID string, found bool, err error) {
	id := strings.TrimSuffix(strings.TrimPrefix(messageID, "<"), ">")
	var out struct {
		Messages []struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
	}
	q := url.Values{"q": {"rfc822msgid:" + id}, "includeSpamTrash": {"true"}, "maxResults": {"1"}}
	if err := s.call(ctx, http.MethodGet, "/users/me/messages", q, nil, &out); err != nil {
		return "", "", false, err
	}
	if len(out.Messages) == 0 {
		return "", "", false, nil
	}
	return out.Messages[0].ID, out.Messages[0].ThreadID, true, nil
}

// Modify adds and removes label ids on a message.
func (s *Service) Modify(ctx context.Context, gmailID string, add, remove []string) error {
	body := map[string]any{"addLabelIds": nonNil(add), "removeLabelIds": nonNil(remove)}
	return s.call(ctx, http.MethodPost, "/users/me/messages/"+url.PathEscape(gmailID)+"/modify", nil, body, nil)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// GmailLabel is a label as Gmail reports it.
type GmailLabel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // system | user
}

func (s *Service) listLabelsAPI(ctx context.Context) ([]GmailLabel, error) {
	var out struct {
		Labels []GmailLabel `json:"labels"`
	}
	if err := s.call(ctx, http.MethodGet, "/users/me/labels", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Labels, nil
}

func (s *Service) createLabelAPI(ctx context.Context, name string) (GmailLabel, error) {
	var l GmailLabel
	err := s.call(ctx, http.MethodPost, "/users/me/labels", nil,
		map[string]any{"name": name, "labelListVisibility": "labelShow", "messageListVisibility": "show"}, &l)
	return l, err
}

// ThreadMessage is one message of a Gmail thread (metadata only).
type ThreadMessage struct {
	ID       string   `json:"id"`
	LabelIDs []string `json:"labelIds"`
}

// GetThread lists the messages of a thread.
func (s *Service) GetThread(ctx context.Context, threadID string) ([]ThreadMessage, error) {
	var out struct {
		Messages []ThreadMessage `json:"messages"`
	}
	q := url.Values{"format": {"minimal"}}
	if err := s.call(ctx, http.MethodGet, "/users/me/threads/"+url.PathEscape(threadID), q, nil, &out); err != nil {
		return nil, err
	}
	return out.Messages, nil
}

// RawMessage is a message fetched with format=raw.
type RawMessage struct {
	ID       string
	ThreadID string
	LabelIDs []string
	Date     time.Time // internalDate
	Raw      []byte
}

// GetRaw downloads a message's RFC 5322 bytes.
func (s *Service) GetRaw(ctx context.Context, gmailID string) (RawMessage, error) {
	var out struct {
		ID           string   `json:"id"`
		ThreadID     string   `json:"threadId"`
		LabelIDs     []string `json:"labelIds"`
		InternalDate string   `json:"internalDate"`
		Raw          string   `json:"raw"`
	}
	if err := s.call(ctx, http.MethodGet, "/users/me/messages/"+url.PathEscape(gmailID), url.Values{"format": {"raw"}}, nil, &out); err != nil {
		return RawMessage{}, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(out.Raw, "="))
	if err != nil {
		return RawMessage{}, fmt.Errorf("gmail: bad raw encoding: %w", err)
	}
	m := RawMessage{ID: out.ID, ThreadID: out.ThreadID, LabelIDs: out.LabelIDs, Raw: raw}
	if ms, err := strconv.ParseInt(out.InternalDate, 10, 64); err == nil {
		m.Date = time.UnixMilli(ms).UTC()
	}
	return m, nil
}

// ListPage is one page of message ids.
type ListPage struct {
	IDs           []string
	NextPageToken string
	Estimate      int
}

// ListMessages runs a Gmail search (q) and returns one page of ids.
func (s *Service) ListMessages(ctx context.Context, q, pageToken string, max int) (ListPage, error) {
	var out struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
		NextPageToken string `json:"nextPageToken"`
		Estimate      int    `json:"resultSizeEstimate"`
	}
	v := url.Values{"q": {q}, "maxResults": {strconv.Itoa(max)}}
	if pageToken != "" {
		v.Set("pageToken", pageToken)
	}
	if err := s.call(ctx, http.MethodGet, "/users/me/messages", v, nil, &out); err != nil {
		return ListPage{}, err
	}
	p := ListPage{NextPageToken: out.NextPageToken, Estimate: out.Estimate}
	for _, m := range out.Messages {
		p.IDs = append(p.IDs, m.ID)
	}
	return p, nil
}

// RawFilter is a Gmail filter as the API returns it.
type RawFilter struct {
	ID       string `json:"id"`
	Criteria struct {
		From           string `json:"from"`
		To             string `json:"to"`
		Subject        string `json:"subject"`
		Query          string `json:"query"`
		NegatedQuery   string `json:"negatedQuery"`
		HasAttachment  bool   `json:"hasAttachment"`
		ExcludeChats   bool   `json:"excludeChats"`
		Size           int    `json:"size"`
		SizeComparison string `json:"sizeComparison"`
	} `json:"criteria"`
	Action struct {
		AddLabelIDs    []string `json:"addLabelIds"`
		RemoveLabelIDs []string `json:"removeLabelIds"`
		Forward        string   `json:"forward"`
	} `json:"action"`
}

func (s *Service) listFiltersAPI(ctx context.Context) ([]RawFilter, error) {
	var out struct {
		Filter []RawFilter `json:"filter"`
	}
	if err := s.call(ctx, http.MethodGet, "/users/me/settings/filters", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Filter, nil
}
