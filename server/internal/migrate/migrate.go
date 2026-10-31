// Package migrate applies embedded goose migrations.
package migrate

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/casprine/inbox/server/db"
)

// Up applies all pending migrations.
func Up(ctx context.Context, databaseURL string) error {
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
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
