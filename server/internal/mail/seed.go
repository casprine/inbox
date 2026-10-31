package mail

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest"
	"github.com/casprine/inbox/server/internal/ingest/parse"
	"github.com/casprine/inbox/server/internal/store"
)

type demoBusiness struct{ name, domain string }

var demoBusinesses = []demoBusiness{
	{"Acme Studio", "acme-demo.test"},
	{"Northwind Goods", "northwind-demo.test"},
	{"Lumen Labs", "lumen-demo.test"},
}

// SeedDemo creates three demo businesses and loads the parse fixtures as messages through
// the real ingest worker. Safe to re-run: businesses, blob keys and Message-IDs are stable.
func SeedDemo(ctx context.Context, st *store.Store, bl *blob.Store, log *slog.Logger) (int, error) {
	biz := make([]store.Business, len(demoBusinesses))
	for i, d := range demoBusinesses {
		b, err := st.GetBusinessByDomain(ctx, d.domain)
		if err != nil {
			if b, err = st.CreateBusiness(ctx, store.Business{ID: ids.NewV7(), Name: d.name, Domain: d.domain,
				SortOrder: i, Enabled: true, Routing: "ok"}); err != nil {
				return 0, err
			}
		}
		biz[i] = b
	}
	entries, err := parse.Fixtures.ReadDir("testdata")
	if err != nil {
		return 0, err
	}
	w := &ingest.Worker{Store: st, Blob: bl, Router: ingest.RulesRouter{}, Log: log}
	locals := []string{"hello", "billing", "support"}
	now := time.Now().UTC()
	n := 0
	for i, e := range entries {
		raw, err := parse.Fixtures.ReadFile("testdata/" + e.Name())
		if err != nil {
			return n, err
		}
		key := "raw/demo/" + e.Name()
		if err := bl.Put(ctx, key, bytes.NewReader(raw), int64(len(raw)), nil); err != nil {
			return n, err
		}
		b := biz[i%len(biz)]
		if e.Name() == "reply.eml" { // keep the reply with its thread root
			b = biz[(i-1+len(biz))%len(biz)]
		}
		err = w.Process(ctx, ingest.IngestArgs{
			Key: key, EnvFrom: "demo@sender.test", EnvTo: locals[i%len(locals)] + "@" + b.Domain,
			RawSize: int64(len(raw)), ReceivedAt: now.Add(-time.Duration(i+1) * 3 * time.Hour).Format(time.RFC3339),
		})
		if err != nil {
			return n, fmt.Errorf("seed %s: %w", e.Name(), err)
		}
		n++
	}
	extra, err := seedExtras(ctx, st, bl, w, biz, now)
	return n + extra, err
}
