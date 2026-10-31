// Package store holds hand-written pgx queries. Query funcs hang off Queries,
// which runs against either the pool or a transaction (see WithTx).
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a single-row lookup matches nothing.
var ErrNotFound = errors.New("store: not found")

// DBTX is satisfied by *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Queries is the set of typed queries bound to a DBTX.
type Queries struct{ db DBTX }

// Store owns the pool and exposes Queries on it.
type Store struct {
	*Queries
	pool *pgxpool.Pool
}

// NewPool connects and pings.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	if databaseURL == "" {
		return nil, errors.New("store: database url is empty")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return pool, nil
}

// New wraps an existing pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{Queries: &Queries{db: pool}, pool: pool}
}

// Pool returns the underlying pool.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Close closes the pool.
func (s *Store) Close() { s.pool.Close() }

// WithTx runs fn in a transaction, committing on nil and rolling back otherwise.
func (s *Store) WithTx(ctx context.Context, fn func(q *Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(&Queries{db: tx}); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
