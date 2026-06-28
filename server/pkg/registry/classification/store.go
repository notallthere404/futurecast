package classification

import (
	"log/slog"

	"github.com/notallthere404/futurecast/server/pkg/registry"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store holds the per-classification table queries (read aggregates
// for the dashboard, bulk insert from the inference worker) plus the
// schema-management helpers the system controller uses during
// syncTables.
type Store struct {
	log  *slog.Logger
	pool *pgxpool.Pool
}

// New returns a classification Store scoped to the shared DB pool.
func New(db *registry.DB) *Store {
	return &Store{
		log:  db.Logger().With(slog.String("resource", "classification")),
		pool: db.Pool(),
	}
}
