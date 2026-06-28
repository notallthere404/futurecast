package monitor

import (
	"log/slog"

	"github.com/notallthere404/futurecast/server/pkg/registry"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store holds the monitor_uptime queries: the heartbeat upsert and
// the dashboard's uptime aggregates.
type Store struct {
	log  *slog.Logger
	pool *pgxpool.Pool
}

// New returns a monitor Store scoped to the shared DB pool.
func New(db *registry.DB) *Store {
	return &Store{
		log:  db.Logger().With(slog.String("resource", "monitor")),
		pool: db.Pool(),
	}
}
