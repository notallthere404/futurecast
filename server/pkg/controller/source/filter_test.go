package source

import (
	"testing"

	"github.com/notallthere404/futurecast/server/pkg/config"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func mustParse(t *testing.T, ss ...string) []config.Filter {
	t.Helper()
	fs, err := config.ParseFilters(ss)
	if err != nil {
		t.Fatalf("ParseFilters: %v", err)
	}
	return fs
}

func TestApplyFilters_NoFilters_PassesAll(t *testing.T) {
	t.Parallel()
	arts := []*v1.Article{{ID: "a", Content: "x"}, {ID: "b", Content: "y"}}
	out, errs := applyFilters(nil, arts)
	if len(out) != 2 || errs != nil {
		t.Errorf("out=%d errs=%v, want 2 / nil", len(out), errs)
	}
}

func TestApplyFilters_LengthBounds_DropsOutOfRange(t *testing.T) {
	t.Parallel()
	fs := mustParse(t, "content.len.gte.10", "content.len.lte.20")
	arts := []*v1.Article{
		{ID: "short", Content: "tiny"},                    // 4 chars — drop
		{ID: "mid", Content: "right in the zone"},         // 17 — keep
		{ID: "long", Content: "way over the upper bound"}, // 24 — drop
	}
	out, errs := applyFilters(fs, arts)
	if errs != nil {
		t.Fatalf("errs: %v", errs)
	}
	if len(out) != 1 || out[0].ID != "mid" {
		t.Errorf("out=%+v, want only mid", out)
	}
}

func TestApplyFilters_MultipleFilters_AllMustPass(t *testing.T) {
	t.Parallel()
	// Length AND title-contains.
	fs := mustParse(t, "content.len.gte.5", "title.contains.eq.cyber")
	arts := []*v1.Article{
		{ID: "ok", Title: "cyber threat", Content: "long enough"},
		{ID: "no-cyber", Title: "general news", Content: "long enough"},
		{ID: "too-short", Title: "cyber alert", Content: "hi"},
	}
	out, errs := applyFilters(fs, arts)
	if errs != nil {
		t.Fatalf("errs: %v", errs)
	}
	if len(out) != 1 || out[0].ID != "ok" {
		t.Errorf("out=%+v, want only ok", out)
	}
}

func TestApplyFilters_Negation_FlipsResult(t *testing.T) {
	t.Parallel()
	// !content.contains.spam → drop articles whose content contains "spam".
	fs := mustParse(t, "!content.contains.eq.spam")
	arts := []*v1.Article{
		{ID: "clean", Content: "regular text"},
		{ID: "spammy", Content: "buy now, total spam"},
	}
	out, errs := applyFilters(fs, arts)
	if errs != nil {
		t.Fatalf("errs: %v", errs)
	}
	if len(out) != 1 || out[0].ID != "clean" {
		t.Errorf("out=%+v, want only clean", out)
	}
}

func TestArticleDoc_FieldUnknownReturnsFalse(t *testing.T) {
	t.Parallel()
	d := articleDoc{a: &v1.Article{Title: "t", Content: "c", Link: "l"}}
	if v, ok := d.Field("title"); !ok || v != "t" {
		t.Errorf("title = (%q,%v)", v, ok)
	}
	if _, ok := d.Field("bogus"); ok {
		t.Errorf("bogus field should be missing")
	}
}

func TestFilterRegistry_SetAndGet(t *testing.T) {
	t.Parallel()
	r := newFilterRegistry()
	fs := mustParse(t, "content.len.gte.5")
	r.Set(map[string][]config.Filter{"https://example.test": fs})
	if got := r.Get("https://example.test"); len(got) != 1 {
		t.Errorf("got %d filters, want 1", len(got))
	}
	if got := r.Get("https://nothing.test"); got != nil {
		t.Errorf("unknown URL should return nil, got %+v", got)
	}
}
