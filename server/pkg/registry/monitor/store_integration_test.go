//go:build integration

package monitor

import (
	"testing"
	"time"

	"github.com/notallthere404/futurecast/server/pkg/registry/dbtest"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db := dbtest.MustDB(t)
	dbtest.Reset(t, db.Pool(), "monitor_uptime")
	return New(db)
}

func TestStore_UpsertUptimeEntry_WithinThresholdUpdatesSameRow(t *testing.T) {
	// The upsert function returns the row id; a second call within
	// threshold must return the same id (recent column refreshed),
	// while a call below threshold creates a new row.
	s := newStore(t)

	id1, err := s.UpsertUptimeEntry(t.Context(), 60)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	id2, err := s.UpsertUptimeEntry(t.Context(), 60)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id1 != id2 {
		t.Errorf("within-threshold call returned id %d, want %d", id2, id1)
	}
}

func TestStore_UpsertUptimeEntry_NonPositiveThresholdErrors(t *testing.T) {
	s := newStore(t)
	if _, err := s.UpsertUptimeEntry(t.Context(), 0); err == nil {
		t.Error("expected error for non-positive threshold")
	}
}

func TestStore_SelectUptimeTotal_NoRows(t *testing.T) {
	s := newStore(t)
	start := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	end := time.Now().UTC().Format(time.RFC3339)

	pct, err := s.SelectUptimeTotal(t.Context(), start, end)
	if err != nil {
		t.Fatalf("SelectUptimeTotal: %v", err)
	}
	if pct != 0 {
		t.Errorf("empty table should yield 0%%, got %f", pct)
	}
}

func TestStore_SelectUptimeSegment_DayBuckets(t *testing.T) {
	s := newStore(t)
	if _, err := s.UpsertUptimeEntry(t.Context(), 60); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}
	got, err := s.SelectUptimeSegment(t.Context(), v1.Day)
	if err != nil {
		t.Fatalf("SelectUptimeSegment: %v", err)
	}
	if len(got) != 144 {
		t.Errorf("day format should produce 144 buckets, got %d", len(got))
	}
}

func TestStore_SelectUptimeSegment_UnsupportedFormat(t *testing.T) {
	s := newStore(t)
	if _, err := s.SelectUptimeSegment(t.Context(), v1.RateFormat("year")); err == nil {
		t.Error("expected error for unsupported format")
	}
}
