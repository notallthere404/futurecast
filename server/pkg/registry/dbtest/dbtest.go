//go:build integration

// Package dbtest; shared scaffolding for integration tests that hit
// the real Postgres. Each store package gets its own *_integration_test.go
// guarded by `//go:build integration`, and uses MustDB + Reset to open
// a connection and clear tables between runs.
//
// Set TEST_DATABASE_URL to point at a DB matching the production schema
// (e.g. the compose-managed `db` service exposed on localhost:7433):
//
//	export TEST_DATABASE_URL='postgresql://db-adm:...@localhost:7433/store?sslmode=disable'
//	go test -tags=integration ./pkg/registry/...
//
// If the env var is not set the tests Skip; keeps the default `go test`
// path fast and dependency-free.
package dbtest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/notallthere404/futurecast/server/pkg/registry"

	"github.com/jackc/pgx/v5/pgxpool"
)

const envKey = "TEST_DATABASE_URL"

var (
	dbOnce sync.Once
	db     *registry.DB
	dbErr  error
)

// MustDB opens (once per test binary) a shared *registry.DB.
// Subsequent callers in the same binary receive the same instance.
// Tests skip when no DSN is configured so the default `go test` path
// stays clean.
func MustDB(t *testing.T) *registry.DB {
	t.Helper()
	dsn := os.Getenv(envKey)
	if dsn == "" {
		t.Skipf("integration: %s not set", envKey)
	}
	dbOnce.Do(func() {
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		db, dbErr = registry.New(log, dsn)
		if dbErr != nil {
			return
		}
		dbErr = db.EnsureBaseSchema(context.Background())
	})
	if dbErr != nil {
		t.Fatalf("integration: open db: %v", dbErr)
	}
	return db
}

// MustPool convenience for tests that only need the bare pool.
func MustPool(t *testing.T) *pgxpool.Pool {
	return MustDB(t).Pool()
}

// Reset clears the given tables. Uses DELETE rather than TRUNCATE so
// the row-level locks don't conflict with a concurrently-running app
// holding ACCESS SHARE on the same tables (the compose `server` stays
// up during local test runs). Slower than TRUNCATE but safe.
//
// Tables listed last are deleted first, so callers can pass parent
// tables first and rely on natural ordering for FK-friendly cleanup; // e.g. Reset(t, pool, "sources", "source_urls") clears source_urls
// (the FK child) before sources.
func Reset(t *testing.T, p *pgxpool.Pool, tables ...string) {
	t.Helper()
	ctx := context.Background()
	for i := len(tables) - 1; i >= 0; i-- {
		q := fmt.Sprintf("DELETE FROM %s", tables[i])
		if _, err := p.Exec(ctx, q); err != nil {
			t.Fatalf("integration: reset %s: %v", tables[i], err)
		}
	}
	_ = strings.Join // retained import for future TRUNCATE fallback
}

// DropIfExists convenience for ad-hoc tables created mid-test
// (e.g. classification schema). Safe to call on a missing table.
//
// Retries on deadlock: dropping a table with an FK to articles forces
// PostgreSQL to take a brief AccessExclusiveLock on articles, which
// can collide with the live compose `server` reading from it. PG
// breaks the cycle by aborting one party; the retry then wins.
func DropIfExists(t *testing.T, p *pgxpool.Pool, tables ...string) {
	t.Helper()
	for _, table := range tables {
		var lastErr error
		for attempt := range 5 {
			_, lastErr = p.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", table))
			if lastErr == nil {
				break
			}
			if !strings.Contains(lastErr.Error(), "deadlock") {
				break
			}
			time.Sleep(time.Duration(50*(attempt+1)) * time.Millisecond)
		}
		if lastErr != nil {
			t.Fatalf("integration: drop %s: %v", table, lastErr)
		}
	}
}
