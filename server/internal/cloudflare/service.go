package cloudflare

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/events"
	"github.com/casprine/inbox/server/internal/store"
)

// Service combines the Cloudflare client with the businesses table.
type Service struct {
	Client *Client
	Store  *store.Store
	Events events.Publisher // optional
	Log    *slog.Logger
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Configured reports whether Cloudflare access is set up.
func (s *Service) Configured() bool { return s != nil && s.Client.Configured() }

// Domain is one Cloudflare zone as the domain picker (OB02) shows it.
type Domain struct {
	ZoneID       string `json:"zone_id"`
	Domain       string `json:"domain"`
	Status       string `json:"status"`
	Routing      string `json:"routing"`
	AlreadyAdded bool   `json:"already_added"`
	// Selectable is true when the zone is active, routes mail to Inbox, and is not a business yet.
	Selectable bool `json:"selectable"`
}

// Domains lists the zones with their routing state, checking up to five at a time.
func (s *Service) Domains(ctx context.Context) ([]Domain, error) {
	zones, err := s.Client.Zones(ctx)
	if err != nil {
		return nil, err
	}
	bs, err := s.Store.ListBusinesses(ctx)
	if err != nil {
		return nil, err
	}
	added := map[string]bool{}
	for _, b := range bs {
		added[strings.ToLower(b.Domain)] = true
	}
	out := make([]Domain, len(zones))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for i, z := range zones {
		out[i] = Domain{ZoneID: z.ID, Domain: strings.ToLower(z.Name), Status: z.Status, Routing: RoutingUnknown}
		out[i].AlreadyAdded = added[out[i].Domain]
		if z.Status != "active" {
			continue
		}
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r, err := s.Client.RoutingState(ctx, id)
			if err != nil {
				s.log().Warn("routing check failed", "zone", id, "err", err)
				return
			}
			out[i].Routing = r
		}(i, z.ID)
	}
	wg.Wait()
	for i := range out {
		out[i].Selectable = out[i].Status == "active" && out[i].Routing == RoutingOK && !out[i].AlreadyAdded
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

// CheckZone checks one zone by id.
func (s *Service) CheckZone(ctx context.Context, zoneID string) (Domain, error) {
	zones, err := s.Client.Zones(ctx)
	if err != nil {
		return Domain{}, err
	}
	for _, z := range zones {
		if z.ID != zoneID {
			continue
		}
		r, err := s.Client.RoutingState(ctx, zoneID)
		if err != nil {
			return Domain{}, err
		}
		b, err := s.Store.GetBusinessByDomain(ctx, strings.ToLower(z.Name))
		added := err == nil && b.ID != uuid.Nil
		return Domain{ZoneID: z.ID, Domain: strings.ToLower(z.Name), Status: z.Status, Routing: r, AlreadyAdded: added,
			Selectable: z.Status == "active" && r == RoutingOK && !added}, nil
	}
	return Domain{}, ErrNotFound
}

// ResolveZone finds the zone for a domain and its routing state (used when a business is added).
func (s *Service) ResolveZone(ctx context.Context, domain string) (zoneID, routing string, found bool, err error) {
	z, err := s.Client.ZoneByName(ctx, domain)
	if errors.Is(err, ErrNotFound) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	r, err := s.Client.RoutingState(ctx, z.ID)
	if err != nil {
		r = RoutingUnknown
	}
	return z.ID, r, true, nil
}

// CheckBusiness re-checks a business's routing, stores the result and announces a change.
func (s *Service) CheckBusiness(ctx context.Context, id uuid.UUID) (routing string, checkedAt time.Time, err error) {
	b, err := s.Store.GetBusiness(ctx, id)
	if err != nil {
		return "", time.Time{}, err
	}
	zone := b.CFZoneID
	if zone == "" {
		z, err := s.Client.ZoneByName(ctx, b.Domain)
		if err != nil {
			return "", time.Time{}, err
		}
		zone = z.ID
		if err := s.Store.SetBusinessZone(ctx, id, zone); err != nil {
			return "", time.Time{}, err
		}
	}
	routing, err = s.Client.RoutingState(ctx, zone)
	if err != nil {
		return "", time.Time{}, err
	}
	prev, err := s.Store.UpdateBusinessRouting(ctx, id, routing)
	if err != nil {
		return "", time.Time{}, err
	}
	if prev != routing && s.Events != nil {
		s.Events.Publish(events.New(events.TypeHealthChanged, map[string]any{"reason": "routing", "business_id": id, "routing": routing}))
	}
	return routing, time.Now().UTC(), nil
}

// CheckAll re-checks every enabled business; failures are logged and skipped.
func (s *Service) CheckAll(ctx context.Context) error {
	if !s.Configured() {
		return nil
	}
	bs, err := s.Store.ListBusinesses(ctx)
	if err != nil {
		return err
	}
	for _, b := range bs {
		if !b.Enabled {
			continue
		}
		if _, _, err := s.CheckBusiness(ctx, b.ID); err != nil {
			s.log().Warn("routing check failed", "business", b.ID, "err", err)
		}
	}
	return nil
}
