//go:build integration

package classification

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/notallthere404/futurecast/server/pkg/registry/dbtest"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(dbtest.MustDB(t))
}

// System tables that must NEVER appear in SelectTableAll; the system
// controller drops anything returned here that isn't a configured
// classification, so a missing exclusion would silently destroy data.
var systemTables = []string{
	"sources", "source_urls", "articles", "monitor_uptime", "views",
}

func TestStore_SelectTableAll_ExcludesSystemTables(t *testing.T) {
	s := newStore(t)

	got, err := s.SelectTableAll(t.Context())
	if err != nil {
		t.Fatalf("SelectTableAll: %v", err)
	}

	for _, sys := range systemTables {
		if _, found := got[sys]; found {
			t.Errorf("system table %q must be excluded from SelectTableAll", sys)
		}
	}
}

func TestStore_CreateTable_DropTable_RoundTrip(t *testing.T) {
	s := newStore(t)
	name := "events_test_int"
	dbtest.DropIfExists(t, s.pool, name+"_metrics", name)
	t.Cleanup(func() { dbtest.DropIfExists(t, s.pool, name+"_metrics", name) })

	if err := s.CreateTable(t.Context(), name); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	all, err := s.SelectTableAll(t.Context())
	if err != nil {
		t.Fatalf("SelectTableAll: %v", err)
	}
	if _, ok := all[name]; !ok {
		t.Errorf("created table %q missing from SelectTableAll: %v", name, all)
	}
	if _, ok := all[name+"_metrics"]; ok {
		t.Errorf("_metrics companion must be filtered out, got %q", name+"_metrics")
	}

	if !tableExists(t, s.pool, name) || !tableExists(t, s.pool, name+"_metrics") {
		t.Fatal("expected both base + _metrics table to exist after CreateTable")
	}

	// DropTable needs AccessExclusiveLock on the referenced articles
	// table (FK). The live compose `server` can race; retry on
	// deadlock so this test stays green under realistic conditions.
	// NOTE: the production DropTable does not retry; a worth-tracking
	// caveat in the real system controller.
	if err := dropWithRetry(t, s, name); err != nil {
		t.Fatalf("DropTable: %v", err)
	}

	if tableExists(t, s.pool, name) || tableExists(t, s.pool, name+"_metrics") {
		t.Errorf("DropTable left tables behind")
	}
}

func TestStore_CreateTable_Idempotent(t *testing.T) {
	s := newStore(t)
	name := "events_idem"
	dbtest.DropIfExists(t, s.pool, name+"_metrics", name)
	t.Cleanup(func() { dbtest.DropIfExists(t, s.pool, name+"_metrics", name) })

	if err := s.CreateTable(t.Context(), name); err != nil {
		t.Fatalf("first CreateTable: %v", err)
	}
	if err := s.CreateTable(t.Context(), name); err != nil {
		t.Errorf("second CreateTable must be no-op (IF NOT EXISTS), got %v", err)
	}
}

func dropWithRetry(t *testing.T, s *Store, name string) error {
	t.Helper()
	var last error
	for attempt := range 5 {
		last = s.DropTable(t.Context(), name)
		if last == nil {
			return nil
		}
		if !strings.Contains(last.Error(), "deadlock") {
			return last
		}
		time.Sleep(time.Duration(100*(attempt+1)) * time.Millisecond)
	}
	return last
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM pg_catalog.pg_tables
			WHERE schemaname = 'public' AND tablename = $1
		)`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("tableExists(%s): %v", name, err)
	}
	return exists
}
