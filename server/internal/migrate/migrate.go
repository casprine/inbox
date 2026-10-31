// Package migrate applies embedded goose migrations.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/casprine/inbox/server/db"
	"github.com/casprine/inbox/server/internal/httpapi"
)

// advisoryKey identifies the migration lock ("inbox mig" as an int64).
const advisoryKey int64 = 0x696e626f786d6967

// Up applies all pending migrations under a Postgres advisory lock, so two processes starting together (a
// restart overlapping a manual `inboxd migrate`, a second replica) take turns instead of racing. It is
// idempotent: with nothing pending it changes nothing. It waits up to a minute for the database to accept
// connections (the container may start before Postgres is ready).
func Up(ctx context.Context, databaseURL string) error {
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	lock, err := acquire(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() {
		// Closing the session releases the lock even if the unlock statement fails.
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = lock.Exec(uctx, "SELECT pg_advisory_unlock($1)", advisoryKey)
		_ = lock.Close(uctx)
	}()
	return up(ctx, databaseURL)
}

func acquire(ctx context.Context, databaseURL string) (*pgx.Conn, error) {
	var last error
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); {
		conn, err := pgx.Connect(ctx, databaseURL)
		if err == nil {
			if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryKey); err == nil {
				return conn, nil
			}
			_ = conn.Close(ctx)
		}
		last = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("database not reachable for migrations: %w", httpapi.ScrubURL(last))
}

func up(ctx context.Context, databaseURL string) error {
	conn, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = stdlib.GetDefaultDriver // ensure pgx stdlib registered
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.UpContext(ctx, conn, "migrations"); err != nil {
		return err
	}
	return riverUp(ctx, databaseURL)
}

// riverUp applies River's own schema migrations.
func riverUp(ctx context.Context, databaseURL string) error {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	m, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return err
	}
	_, err = m.Migrate(ctx, rivermigrate.DirectionUp, nil)
	return err
}
