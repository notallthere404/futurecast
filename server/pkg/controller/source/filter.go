package source

import (
	"fmt"
	"sync"

	"github.com/notallthere404/futurecast/server/pkg/config"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

// filterRegistry keeps the active filter set per source URL. The
// system controller rebuilds it after every config (re)load by
// merging per-kind default + per-source override and parsing the
// strings into config.Filter; the source controller queries it
// before inserting articles.
//
// URL is the key because it's the natural unique identifier shared
// by config + DB rows (sources.url has a UNIQUE constraint). Tests
// can call SetFilters with whatever map shape they need.
type filterRegistry struct {
	mu    sync.RWMutex
	byURL map[string][]config.Filter
}

func newFilterRegistry() *filterRegistry {
	return &filterRegistry{byURL: make(map[string][]config.Filter)}
}

// Set replaces the entire filter set atomically. Callers compile the
// raw strings via config.ParseFilters before handing in.
func (r *filterRegistry) Set(m map[string][]config.Filter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byURL = m
}

// Get returns the filter list for a source URL. Empty slice when no
// filters are configured for that source.
func (r *filterRegistry) Get(url string) []config.Filter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byURL[url]
}

// articleDoc adapts v1.Article to the config.Doc interface so the
// filter evaluator can read its fields. Only the article fields used
// by today's filters (title, content, link) are surfaced; expand the
// switch as new filter targets are needed.
type articleDoc struct{ a *v1.Article }

func (d articleDoc) Field(name string) (string, bool) {
	switch name {
	case "title":
		return d.a.Title, true
	case "content":
		return d.a.Content, true
	case "link":
		return d.a.Link, true
	}
	return "", false
}

func (d articleDoc) Fields() []string {
	return []string{"title", "content", "link"}
}

// applyFilters drops articles that fail any of the source's
// configured filters. Returns the surviving slice unchanged when no
// filters are configured. A filter that errors during evaluation is
// treated as a rejection so a malformed regex doesn't quietly let
// junk through.
func applyFilters(fs []config.Filter, arts []*v1.Article) ([]*v1.Article, []error) {
	if len(fs) == 0 {
		return arts, nil
	}
	out := make([]*v1.Article, 0, len(arts))
	var errs []error
	for _, a := range arts {
		ok, err := evalAll(fs, a)
		if err != nil {
			errs = append(errs, fmt.Errorf("article %s: %w", a.ID, err))
			continue
		}
		if ok {
			out = append(out, a)
		}
	}
	return out, errs
}

func evalAll(fs []config.Filter, a *v1.Article) (bool, error) {
	doc := articleDoc{a: a}
	for _, f := range fs {
		ok, err := f.Eval(doc)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}
