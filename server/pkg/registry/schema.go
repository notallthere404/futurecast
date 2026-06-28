package registry

import (
	"context"
	_ "embed"
	"fmt"
)

//go:embed base.sql
var baseSchema string

// EnsureBaseSchema runs the embedded base-schema DDL against the
// connected database. The DDL is idempotent; every CREATE uses
// IF NOT EXISTS (or a DO block for ENUM types), so repeated calls
// on an already-initialised database are safe and cheap.
//
// Called once at server startup so external/managed Postgres instances
// bootstrap without the docker-compose initdb mount.
func (db *DB) EnsureBaseSchema(ctx context.Context) error {
	if _, err := db.pool.Exec(ctx, baseSchema); err != nil {
		return fmt.Errorf("apply base schema: %w", err)
	}
	db.log.Info("base schema ensured")
	return nil
}
