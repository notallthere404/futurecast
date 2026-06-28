package registry

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps the connection pool plus a scoped logger. Per-resource
// stores construct themselves against one shared DB so the pool is
// shared process-wide.
type DB struct {
	log  *slog.Logger
	pool *pgxpool.Pool
}

// New opens a connection pool against the given DSN. Callers resolve
// the DSN themselves (config.Server.ExtDb or the DATABASE_URL env)
// before invoking; passing an empty string is a configuration error.
func New(log *slog.Logger, dsn string) (*DB, error) {
	if dsn == "" {
		log.Error("database dsn not provided")
		return nil, errors.New("database dsn not provided")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, err
	}

	return &DB{
		log:  log.With(slog.String("mod", "registry")),
		pool: pool,
	}, nil
}

// Close drains and closes the underlying connection pool.
func (db *DB) Close() {
	db.pool.Close()
}

// Pool returns the shared pgxpool. Per-resource stores use this
// directly for their query methods.
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

// Logger returns the registry's root logger. Per-resource stores
// derive a scoped child via Logger().With("resource", "<name>").
func (db *DB) Logger() *slog.Logger {
	return db.log
}

// Base is the shared shape every resource store embeds: a scoped logger and
// the shared pool. Resource packages embed this and add their query methods.
//
// Generic factory NewStore[T] is not feasible in Go without reflection because
// T's fields cannot be set generically. Embedding Base is the idiomatic path.
type Base struct {
	log  *slog.Logger
	pool *pgxpool.Pool
}

// NewBase produces a Base scoped to a resource. Stores construct themselves
// with `store := &Store{Base: registry.NewBase(db, "<resource>")}`.
func NewBase(db *DB, resource string) Base {
	return Base{
		log:  db.Logger().With(slog.String("resource", resource)),
		pool: db.Pool(),
	}
}
