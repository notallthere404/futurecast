package article

import (
	"log/slog"

	"github.com/notallthere404/futurecast/server/pkg/registry"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store holds the article-table queries: batch insert, selection for
// the inference worker's refill, and the dashboard's recent + rate
// reads.
type Store struct {
	log  *slog.Logger
	pool *pgxpool.Pool
}

// New returns an article Store scoped to the shared DB pool.
func New(db *registry.DB) *Store {
	return &Store{
		log:  db.Logger().With(slog.String("resource", "article")),
		pool: db.Pool(),
	}
}
