package source

import (
	"log/slog"

	"github.com/notallthere404/futurecast/server/pkg/registry"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store holds the source-table queries: per-type and full reads,
// single + bulk upsert, and the URL-set cleanup the system controller
// runs during syncSources.
type Store struct {
	log  *slog.Logger
	pool *pgxpool.Pool
}

// New returns a source Store scoped to the shared DB pool.
func New(db *registry.DB) *Store {
	return &Store{
		log:  db.Logger().With(slog.String("resource", "source")),
		pool: db.Pool(),
	}
}
