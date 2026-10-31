package gmail

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/store"
)

func (s *Service) account(ctx context.Context) (store.GmailAccount, error) {
	a, err := s.Store.GetGmailAccount(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return a, ErrNoAccount
	}
	return a, err
}

// RefreshLabels reloads the user labels from Gmail into the cache and returns name -> id.
func (s *Service) RefreshLabels(ctx context.Context) (map[string]string, error) {
	acct, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	return s.refreshLabels(ctx, acct.ID)
}

func (s *Service) refreshLabels(ctx context.Context, account uuid.UUID) (map[string]string, error) {
	labels, err := s.listLabelsAPI(ctx)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, l := range labels {
		if l.Type != "system" {
			m[l.Name] = l.ID
		}
	}
	if err := s.Store.ReplaceGmailLabels(ctx, account, m); err != nil {
		return nil, err
	}
	return m, nil
}

// ResolveLabel returns the Gmail label id for name, creating the label (once) if it does not exist.
// Nested names ("Acme/Invoices") are single labels in Gmail's model; it shows the hierarchy itself.
func (s *Service) ResolveLabel(ctx context.Context, name string) (string, error) {
	acct, err := s.account(ctx)
	if err != nil {
		return "", err
	}
	cached, err := s.Store.GmailLabels(ctx, acct.ID)
	if err != nil {
		return "", err
	}
	if id, ok := cached[name]; ok {
		return id, nil
	}
	fresh, err := s.refreshLabels(ctx, acct.ID)
	if err != nil {
		return "", err
	}
	if id, ok := fresh[name]; ok {
		return id, nil
	}
	l, err := s.createLabelAPI(ctx, name)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusConflict {
		// Created concurrently or differs only by case: reload and look again.
		if fresh, err = s.refreshLabels(ctx, acct.ID); err != nil {
			return "", err
		}
		if id, ok := fresh[name]; ok {
			return id, nil
		}
		return "", ae
	}
	if err != nil {
		return "", err
	}
	if err := s.Store.UpsertGmailLabel(ctx, acct.ID, name, l.ID); err != nil {
		return "", err
	}
	return l.ID, nil
}

// CachedLabelID returns a cached label id without calling Gmail.
func (s *Service) CachedLabelID(ctx context.Context, name string) (string, bool) {
	acct, err := s.account(ctx)
	if err != nil {
		return "", false
	}
	m, err := s.Store.GmailLabels(ctx, acct.ID)
	if err != nil {
		return "", false
	}
	id, ok := m[name]
	return id, ok
}

// ForgetLabels clears the label cache (a cached id turned out to be stale).
func (s *Service) ForgetLabels(ctx context.Context) {
	if acct, err := s.account(ctx); err == nil {
		_ = s.Store.ReplaceGmailLabels(ctx, acct.ID, nil)
	}
}

// SortedNames returns the keys of m sorted.
func SortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
