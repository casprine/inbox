package gmail

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/store"
)

// AccessToken returns a valid access token for the connected account, refreshing when needed.
// An `invalid_grant` answer marks the account needs_reauth, pauses queued write-backs and
// returns ErrNeedsReauth; a missing account returns ErrNoAccount.
func (s *Service) AccessToken(ctx context.Context) (string, error) {
	if !s.Configured() {
		return "", ErrNotConfigured
	}
	acct, err := s.Store.GetGmailAccount(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrNoAccount
	}
	if err != nil {
		return "", err
	}
	if acct.Status != "connected" {
		return "", ErrNeedsReauth
	}
	s.mu.Lock()
	if c, ok := s.access[acct.ID.String()]; ok && time.Until(c.expires) > time.Minute {
		s.mu.Unlock()
		return c.token, nil
	}
	s.mu.Unlock()

	refresh, err := s.Keys.Open(acct.RefreshTokenEnc, []byte(tokenAAD))
	if err != nil {
		return "", err
	}
	tok, err := s.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {string(refresh)}})
	if err != nil {
		var te *TokenError
		if errors.As(err, &te) && te.Code == "invalid_grant" {
			s.markNeedsReauth(ctx, acct)
			return "", ErrNeedsReauth
		}
		return "", err
	}
	s.cacheToken(acct.ID, tok)
	return tok.AccessToken, nil
}

func (s *Service) markNeedsReauth(ctx context.Context, acct store.GmailAccount) {
	ctx = context.WithoutCancel(ctx)
	err := s.Store.WithTx(ctx, func(q *store.Queries) error {
		if _, err := q.SetGmailAccountStatus(ctx, acct.ID, "needs_reauth"); err != nil {
			return err
		}
		_, err := q.PauseGmailSync(ctx)
		return err
	})
	if err != nil {
		s.log().Error("mark needs_reauth", "err", err)
		return
	}
	s.mu.Lock()
	delete(s.access, acct.ID.String())
	s.mu.Unlock()
	s.publishStatus(ctx)
}

// Disconnect revokes the token at Google (best effort), forgets the account and its label
// cache, and pauses queued write-backs. Returns false when nothing was connected.
func (s *Service) Disconnect(ctx context.Context) (bool, error) {
	var acct store.GmailAccount
	err := s.Store.WithTx(ctx, func(q *store.Queries) error {
		var err error
		if acct, err = q.DeleteGmailAccount(ctx); err != nil {
			return err
		}
		_, err = q.PauseGmailSync(ctx)
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	delete(s.access, acct.ID.String())
	s.mu.Unlock()
	if s.Keys != nil {
		if refresh, err := s.Keys.Open(acct.RefreshTokenEnc, []byte(tokenAAD)); err == nil {
			s.revoke(ctx, string(refresh))
		}
	}
	s.publishStatus(ctx)
	return true, nil
}

func (s *Service) revoke(ctx context.Context, token string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	// The token goes in the body, not the URL: URLs end up in logs and error messages.
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, s.Cfg.revokeURL(), strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http().Do(req)
	if err != nil {
		s.log().Warn("gmail revoke failed", "err", httpapi.ScrubURL(err))
		return
	}
	resp.Body.Close()
}
