//go:build integration

package source

import (
	"strings"
	"testing"

	"github.com/notallthere404/futurecast/server/pkg/registry/dbtest"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db := dbtest.MustDB(t)
	dbtest.Reset(t, db.Pool(), "sources")
	return New(db)
}

// makeRSS a populated RSS source covering every column. Tests narrow
// fields by overlaying onto this base.
func makeRSS(url, name string) *v1.Source {
	hash, _ := (&v1.RSSSpec{URL: url}).Kind(), [32]byte{}
	_ = hash
	return &v1.Source{
		Type:   v1.RSSType,
		Name:   name,
		URL:    url,
		Hash:   []byte{1, 2, 3, 4},
		Active: true,
		Trust:  v1.High,
		Tags:   []string{"sec", "cve"},
		Spec: &v1.RSSSpec{
			URL:      url,
			Target:   v1.DescriptionTarget,
			Schedule: "*/10 * * * *",
		},
	}
}

func TestStore_UpsertSource_TagsRequired(t *testing.T) {
	// sources.tags is NOT NULL DEFAULT '{}'. Inserting with Tags=nil
	// must fail; verifies the Resolve()-side normalisation is doing
	// the work it claims to do and the column hasn't silently grown
	// a more permissive constraint.
	s := newStore(t)
	src := makeRSS("https://x.test/feed", "x")
	src.Tags = nil
	err := s.UpsertSource(t.Context(), src)
	if err == nil {
		t.Fatal("expected NOT NULL violation when Tags is nil")
	}
	if !strings.Contains(err.Error(), "tags") {
		t.Errorf("err = %v, want mention of 'tags' column", err)
	}
}

func TestStore_UpsertSource_RoundTrip(t *testing.T) {
	s := newStore(t)
	want := makeRSS("https://cisa.test/feed", "CISA")

	if err := s.UpsertSource(t.Context(), want); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	got, err := s.SelectSourceAll(t.Context())
	if err != nil {
		t.Fatalf("SelectSourceAll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if diff := cmp.Diff(want, got[0],
		cmpopts.IgnoreFields(v1.Source{}, "ID", "CreatedAt", "UpdatedAt", "SpecRaw"),
	); diff != "" {
		t.Errorf("source round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestStore_UpsertSource_SpecRoundTripsViaJSONB(t *testing.T) {
	s := newStore(t)
	src := makeRSS("https://feed.test/", "Feed")
	src.Spec = &v1.RSSSpec{
		URL:      "https://feed.test/",
		Target:   v1.ContentTarget,
		Schedule: "*/5 * * * *",
		Paths:    []string{"/a", "/b"},
		Selectors: v1.SelectorMap{
			v1.Title:   {".title"},
			v1.Content: {".body"},
		},
	}
	if err := s.UpsertSource(t.Context(), src); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, _ := s.SelectSourceAll(t.Context())
	rss := got[0].RSS()
	if rss == nil {
		t.Fatal("HydrateSpec did not produce an RSSSpec")
	}
	if rss.Target != v1.ContentTarget || rss.Schedule != "*/5 * * * *" {
		t.Errorf("rss spec lost fields: %+v", rss)
	}
	if diff := cmp.Diff([]string{"/a", "/b"}, rss.Paths); diff != "" {
		t.Errorf("paths mismatch:\n%s", diff)
	}
}

func TestStore_UpsertSource_ConflictOnURL(t *testing.T) {
	// Same URL twice updates rather than duplicating.
	s := newStore(t)
	first := makeRSS("https://dup.test/", "first")
	second := makeRSS("https://dup.test/", "second")
	second.Active = false

	if err := s.UpsertSource(t.Context(), first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := s.UpsertSource(t.Context(), second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, _ := s.SelectSourceAll(t.Context())
	if len(got) != 1 {
		t.Fatalf("conflict should keep 1 row, got %d", len(got))
	}
	if got[0].Name != "second" || got[0].Active {
		t.Errorf("upsert did not overwrite: %+v", got[0])
	}
}

func TestStore_SelectSourceByType_ActiveOnly(t *testing.T) {
	s := newStore(t)
	a := makeRSS("https://a.test/", "a")
	b := makeRSS("https://b.test/", "b")
	b.Active = false
	c := makeRSS("https://c.test/", "c")
	c.Type = v1.WebhookType
	c.Spec = &v1.WebhookSpec{Path: "/c"}

	for _, src := range []*v1.Source{a, b, c} {
		if err := s.UpsertSource(t.Context(), src); err != nil {
			t.Fatalf("upsert %s: %v", src.Name, err)
		}
	}

	got, err := s.SelectSourceByType(t.Context(), v1.RSSType)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(got) != 1 || got[0].Name != "a" {
		t.Errorf("expected only active rss source 'a', got %+v", got)
	}
}

func TestStore_UpsertSourceBatch(t *testing.T) {
	s := newStore(t)
	srcs := []*v1.Source{
		makeRSS("https://b1.test/", "b1"),
		makeRSS("https://b2.test/", "b2"),
		makeRSS("https://b3.test/", "b3"),
	}
	if err := s.UpsertSourceBatch(t.Context(), srcs); err != nil {
		t.Fatalf("UpsertSourceBatch: %v", err)
	}
	got, _ := s.SelectSourceAll(t.Context())
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
}

func TestStore_UpsertSourceBatch_NilTagsFails(t *testing.T) {
	// Batch path uses CopyFrom (binary protocol). A nil Tags slice
	// must surface as a NOT NULL violation, not silently land as NULL.
	// This is the exact failure mode that triggered the original
	// resolveCommon Tags fix.
	s := newStore(t)
	src := makeRSS("https://copy.test/", "copy")
	src.Tags = nil

	err := s.UpsertSourceBatch(t.Context(), []*v1.Source{src})
	if err == nil {
		t.Fatal("expected CopyFrom NOT NULL violation when Tags is nil")
	}
}

func TestStore_DeleteSourceBatch(t *testing.T) {
	s := newStore(t)
	keep := makeRSS("https://keep.test/", "keep")
	gone := makeRSS("https://gone.test/", "gone")
	for _, src := range []*v1.Source{keep, gone} {
		if err := s.UpsertSource(t.Context(), src); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	if err := s.DeleteSourceBatch(t.Context(), []string{"https://gone.test/"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, _ := s.SelectSourceAll(t.Context())
	if len(got) != 1 || got[0].URL != "https://keep.test/" {
		t.Errorf("after delete: %+v", got)
	}
}

func TestStore_DeleteSourceBatch_Empty(t *testing.T) {
	s := newStore(t)
	if err := s.DeleteSourceBatch(t.Context(), nil); err != nil {
		t.Errorf("empty Delete should be a no-op, got err: %v", err)
	}
}
