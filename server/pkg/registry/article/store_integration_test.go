//go:build integration

package article

import (
	"testing"
	"time"

	"github.com/notallthere404/futurecast/server/pkg/registry/dbtest"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db := dbtest.MustDB(t)
	dbtest.Reset(t, db.Pool(), "articles")
	return New(db)
}

func makeArticle(id, title string, ts time.Time, processed bool) *v1.Article {
	return &v1.Article{
		ID:         id,
		SourceID:   "11111111-1111-1111-1111-111111111111",
		SourceType: v1.RSSType,
		Title:      title,
		Content:    "body",
		Timestamp:  ts,
		Link:       "https://example.test/" + id,
		Processed:  processed,
	}
}

func TestStore_InsertArticleBatch_RoundTrip(t *testing.T) {
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	a := makeArticle("11111111-1111-1111-1111-111111111101", "first", now, false)
	b := makeArticle("11111111-1111-1111-1111-111111111102", "second", now.Add(time.Minute), false)

	if err := s.InsertArticleBatch(t.Context(), []*v1.Article{a, b}); err != nil {
		t.Fatalf("InsertArticleBatch: %v", err)
	}

	got, err := s.SelectArticleRecent(t.Context())
	if err != nil {
		t.Fatalf("SelectArticleRecent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	// SelectArticleRecent orders DESC by timestamp.
	if got[0].Title != "second" || got[1].Title != "first" {
		t.Errorf("ordering wrong: %s, %s", got[0].Title, got[1].Title)
	}
}

func TestStore_InsertArticleBatch_DuplicatesIgnored(t *testing.T) {
	// ON CONFLICT (id) DO NOTHING; inserting the same id twice keeps
	// the original row and silently ignores the duplicate.
	s := newStore(t)
	a := makeArticle("22222222-2222-2222-2222-222222222201", "orig", time.Now().UTC(), false)
	dup := makeArticle("22222222-2222-2222-2222-222222222201", "dup", time.Now().UTC(), true)

	if err := s.InsertArticleBatch(t.Context(), []*v1.Article{a}); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := s.InsertArticleBatch(t.Context(), []*v1.Article{dup}); err != nil {
		t.Fatalf("duplicate insert: %v", err)
	}

	got, _ := s.SelectArticleRecent(t.Context())
	if len(got) != 1 {
		t.Fatalf("expected 1 row after dedup, got %d", len(got))
	}
	if got[0].Title != "orig" {
		t.Errorf("dedup should keep original, got Title=%q", got[0].Title)
	}
}

func TestStore_InsertArticleBatch_Empty(t *testing.T) {
	s := newStore(t)
	if err := s.InsertArticleBatch(t.Context(), nil); err != nil {
		t.Errorf("empty batch should be no-op, got: %v", err)
	}
}

func TestStore_UpdateArticleProcessed(t *testing.T) {
	s := newStore(t)
	a := makeArticle("33333333-3333-3333-3333-333333333301", "a", time.Now().UTC(), false)
	b := makeArticle("33333333-3333-3333-3333-333333333302", "b", time.Now().UTC(), false)
	c := makeArticle("33333333-3333-3333-3333-333333333303", "c", time.Now().UTC(), false)

	if err := s.InsertArticleBatch(t.Context(), []*v1.Article{a, b, c}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := s.UpdateArticleProcessed(t.Context(), []string{a.ID, c.ID}); err != nil {
		t.Fatalf("UpdateArticleProcessed: %v", err)
	}

	batch, err := s.SelectArticleBatch(t.Context(), 10)
	if err != nil {
		t.Fatalf("SelectArticleBatch: %v", err)
	}
	if len(batch) != 1 || batch[0].ID != b.ID {
		t.Errorf("only unprocessed b should remain, got %+v", batch)
	}
}

func TestStore_SelectArticleBatch_RespectsLimit(t *testing.T) {
	s := newStore(t)
	articles := make([]*v1.Article, 5)
	for i := range articles {
		articles[i] = makeArticle(
			"44444444-4444-4444-4444-44444444440"+string(rune('0'+i)),
			"a", time.Now().UTC(), false,
		)
	}
	if err := s.InsertArticleBatch(t.Context(), articles); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.SelectArticleBatch(t.Context(), 3)
	if err != nil {
		t.Fatalf("SelectArticleBatch: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d, want 3 (limit)", len(got))
	}
}

func TestStore_SelectArticleRate_Day(t *testing.T) {
	s := newStore(t)
	if err := s.InsertArticleBatch(t.Context(), []*v1.Article{
		makeArticle("55555555-5555-5555-5555-555555555501", "x", time.Now().UTC(), false),
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	counts, err := s.SelectArticleRate(t.Context(), v1.Day)
	if err != nil {
		t.Fatalf("SelectArticleRate: %v", err)
	}
	if len(counts) != 24 {
		t.Errorf("day format should produce 24 buckets, got %d", len(counts))
	}
}

func TestStore_SelectArticleRate_UnsupportedFormat(t *testing.T) {
	s := newStore(t)
	if _, err := s.SelectArticleRate(t.Context(), v1.RateFormat("year")); err == nil {
		t.Error("expected error for unsupported format")
	}
}
