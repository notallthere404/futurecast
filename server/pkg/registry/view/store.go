package view

import (
	"log/slog"

	"github.com/notallthere404/futurecast/server/pkg/registry"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store holds the views-table queries: CRUD plus the slug-keyed read
// the dashboard's view-render path depends on.
type Store struct {
	log  *slog.Logger
	pool *pgxpool.Pool
}

// New returns a view Store scoped to the shared DB pool.
func New(db *registry.DB) *Store {
	return &Store{
		log:  db.Logger().With(slog.String("resource", "view")),
		pool: db.Pool(),
	}
}
