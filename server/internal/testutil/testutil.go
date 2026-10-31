// Package testutil holds helpers shared by integration tests.
package testutil

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/casprine/inbox/server/internal/crypto"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/migrate"
	"github.com/casprine/inbox/server/internal/store"
)

// DB is a migrated throwaway schema plus a registered device.
type DB struct {
	Store *store.Store
	Pool  *pgxpool.Pool
	URL   string
	// Token is a bearer token for a device already in the database.
	Token    string
	DeviceID uuid.UUID
}

// NewDB skips the test when TEST_DATABASE_URL is unset; otherwise it migrates a fresh schema
// (dropped on cleanup) and registers a device.
func NewDB(t *testing.T) *DB {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	schema := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	url := fmt.Sprintf("%s%ssearch_path=%s,public", base, sep, schema)
	if err := migrate.Up(ctx, url); err != nil {
		t.Fatal(err)
	}
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)
	token := "test-token-" + uuid.NewString()
	hash, _ := hex.DecodeString(crypto.HashToken(token))
	dev := ids.NewV7()
	if err := st.CreateDevice(ctx, dev, "test", hash); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return &DB{Store: st, Pool: pool, URL: url, Token: token, DeviceID: dev}
}
