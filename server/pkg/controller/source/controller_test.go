package source

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/notallthere404/futurecast/server/pkg/schema"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

type fakeSourceStore struct {
	all       []*v1.Source
	allErr    error
	byType    map[v1.SourceType][]*v1.Source
	byTypeErr error

	upsertCalls []*v1.Source
	upsertErr   error
	batchCalls  [][]*v1.Source
	batchErr    error
}

func (f *fakeSourceStore) SelectSourceAll(context.Context) ([]*v1.Source, error) {
	return f.all, f.allErr
}

func (f *fakeSourceStore) SelectSourceByType(_ context.Context, t v1.SourceType) ([]*v1.Source, error) {
	return f.byType[t], f.byTypeErr
}

func (f *fakeSourceStore) UpsertSource(_ context.Context, src *v1.Source) error {
	f.upsertCalls = append(f.upsertCalls, src)
	return f.upsertErr
}

func (f *fakeSourceStore) UpsertSourceBatch(_ context.Context, srcs []*v1.Source) error {
	f.batchCalls = append(f.batchCalls, srcs)
	return f.batchErr
}

type fakeArticleStore struct {
	recent      []*v1.Article
	recentErr   error
	rate        []int
	rateErr     error
	rateFmt     []v1.RateFormat
	insertErr   error
	insertCalls [][]*v1.Article
}

func (f *fakeArticleStore) SelectArticleRecent(context.Context) ([]*v1.Article, error) {
	return f.recent, f.recentErr
}

func (f *fakeArticleStore) SelectArticleRate(_ context.Context, format v1.RateFormat) ([]int, error) {
	f.rateFmt = append(f.rateFmt, format)
	return f.rate, f.rateErr
}

func (f *fakeArticleStore) InsertArticleBatch(_ context.Context, arts []*v1.Article) error {
	f.insertCalls = append(f.insertCalls, arts)
	return f.insertErr
}

// fakeInference records every Kick so insert-path tests can assert
// the inference worker is signaled after a successful write.
type fakeInference struct {
	kicks int
}

func (f *fakeInference) Kick() { f.kicks++ }

// newCtrl assembles a Controller directly so we control deps without
// touching the real driver registry (registerDriver lives in the same
// package and is invoked from Register; tests for that path need the
// real *source.Driver and exercise it via the public Register method).
func newCtrl(store Store, articles ArticleStore, g *schema.Guard) *Controller {
	return newCtrlWithInference(store, articles, g, &fakeInference{})
}

func newCtrlWithInference(store Store, articles ArticleStore, g *schema.Guard, inf Inference) *Controller {
	if g == nil {
		g = schema.New()
	}
	return New(discardLogger(), store, articles, nil, g, inf)
}

func TestList_ForwardsToStore(t *testing.T) {
	t.Parallel()
	want := []*v1.Source{{ID: "s1"}}
	store := &fakeSourceStore{all: want}
	ctrl := newCtrl(store, &fakeArticleStore{}, nil)

	got, err := ctrl.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "s1" {
		t.Errorf("got %+v", got)
	}
}

func TestUpsert_ForwardsToStore(t *testing.T) {
	t.Parallel()
	store := &fakeSourceStore{}
	ctrl := newCtrl(store, &fakeArticleStore{}, nil)

	src := &v1.Source{ID: "x"}
	if err := ctrl.Upsert(t.Context(), src); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(store.upsertCalls) != 1 || store.upsertCalls[0] != src {
		t.Errorf("upsert calls = %+v", store.upsertCalls)
	}
}

func TestUpsertBatch_ForwardsToStore(t *testing.T) {
	t.Parallel()
	store := &fakeSourceStore{}
	ctrl := newCtrl(store, &fakeArticleStore{}, nil)

	srcs := []*v1.Source{{ID: "a"}, {ID: "b"}}
	if err := ctrl.UpsertBatch(t.Context(), srcs); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
	if len(store.batchCalls) != 1 || len(store.batchCalls[0]) != 2 {
		t.Errorf("batch calls = %+v", store.batchCalls)
	}
}

func TestRecent_ForwardsAndHoldsGuard(t *testing.T) {
	t.Parallel()
	articles := &fakeArticleStore{recent: []*v1.Article{{ID: "a"}}}
	ctrl := newCtrl(&fakeSourceStore{}, articles, nil)

	got, err := ctrl.Recent(t.Context())
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("got %+v", got)
	}
}

func TestRate_ForwardsFormat(t *testing.T) {
	t.Parallel()
	articles := &fakeArticleStore{rate: []int{1, 2, 3}}
	ctrl := newCtrl(&fakeSourceStore{}, articles, nil)

	got, err := ctrl.Rate(t.Context(), v1.Day)
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %v", got)
	}
	if len(articles.rateFmt) != 1 || articles.rateFmt[0] != v1.Day {
		t.Errorf("forwarded format = %v", articles.rateFmt)
	}
}

func TestInsertBatch_ForwardsToArticleStoreAndKicksInference(t *testing.T) {
	t.Parallel()
	articles := &fakeArticleStore{}
	inf := &fakeInference{}
	ctrl := newCtrlWithInference(&fakeSourceStore{}, articles, nil, inf)

	arts := []*v1.Article{{ID: "a"}, {ID: "b"}}
	if err := ctrl.InsertBatch(t.Context(), arts); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if len(articles.insertCalls) != 1 || len(articles.insertCalls[0]) != 2 {
		t.Errorf("insertCalls = %+v", articles.insertCalls)
	}
	if inf.kicks != 1 {
		t.Errorf("inference kicks = %d, want 1 after successful insert", inf.kicks)
	}
}

func TestInsertBatch_StoreError_NoKick(t *testing.T) {
	// A failed insert must NOT kick the worker; there is nothing new
	// to classify, and a phantom kick would burn a refill cycle for
	// rows that never landed.
	t.Parallel()
	articles := &fakeArticleStore{insertErr: errors.New("dup")}
	inf := &fakeInference{}
	ctrl := newCtrlWithInference(&fakeSourceStore{}, articles, nil, inf)

	if err := ctrl.InsertBatch(t.Context(), []*v1.Article{{ID: "a"}}); err == nil {
		t.Fatal("expected store error to bubble")
	}
	if inf.kicks != 0 {
		t.Errorf("inference kicks = %d, want 0 on insert failure", inf.kicks)
	}
}

func TestInsertBatch_EmptySlice_NoKick(t *testing.T) {
	// Empty batch reaches the store (which no-ops) but must not kick
	// inference there is no new work to refill from.
	t.Parallel()
	articles := &fakeArticleStore{}
	inf := &fakeInference{}
	ctrl := newCtrlWithInference(&fakeSourceStore{}, articles, nil, inf)

	if err := ctrl.InsertBatch(t.Context(), nil); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if inf.kicks != 0 {
		t.Errorf("inference kicks = %d, want 0 on empty batch", inf.kicks)
	}
}

func TestInsertBatch_RLockBlockedByWriter(t *testing.T) {
	// The RLock around InsertBatch is what protects the classifier and
	// source ingest from deadlocking against syncTables. Verify the
	// wiring: a writer holding the guard blocks InsertBatch until release.
	t.Parallel()
	g := schema.New()
	articles := &fakeArticleStore{}
	ctrl := newCtrl(&fakeSourceStore{}, articles, g)

	g.Lock()
	done := make(chan error, 1)
	go func() { done <- ctrl.InsertBatch(context.Background(), []*v1.Article{{ID: "a"}}) }()

	select {
	case <-done:
		t.Fatal("InsertBatch returned while writer held the guard")
	case <-time.After(50 * time.Millisecond):
	}

	g.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("InsertBatch err: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("InsertBatch did not proceed after guard.Unlock")
	}
}

func TestRegister_FiltersInactiveAndDispatchesDrivers(t *testing.T) {
	// Register walks SelectSourceAll, keeps only active sources, and
	// lazily registers each Type's driver exactly once.
	t.Parallel()
	store := &fakeSourceStore{
		all: []*v1.Source{
			{ID: "rss1", Type: v1.RSSType, Active: true},
			{ID: "rss2", Type: v1.RSSType, Active: true}, // same type, driver dedup
			{ID: "rss-off", Type: v1.RSSType, Active: false},
			{
				ID: "wh1", Type: v1.WebhookType, Active: true,
				Spec: &v1.WebhookSpec{Path: "/wh1"},
			},
		},
	}
	ctrl := newCtrl(store, &fakeArticleStore{}, nil)

	active, err := ctrl.Register(t.Context())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(active) != 3 {
		t.Errorf("got %d active, want 3", len(active))
	}
	if _, err := ctrl.driver.Retriever(v1.RSSType); err != nil {
		t.Errorf("rss retriever not registered: %v", err)
	}
	if _, err := ctrl.driver.Listener(v1.WebhookType); err != nil {
		t.Errorf("webhook listener not registered: %v", err)
	}
}

func TestRegister_StoreError_Bubbles(t *testing.T) {
	t.Parallel()
	store := &fakeSourceStore{allErr: errors.New("db down")}
	ctrl := newCtrl(store, &fakeArticleStore{}, nil)
	if _, err := ctrl.Register(t.Context()); err == nil {
		t.Error("expected SelectSourceAll error to bubble")
	}
}

func TestRun_FetchAndInsert(t *testing.T) {
	// Run dispatches via the driver registry built by Register. We
	// inject a fake retriever directly so the test exercises Run's
	// fetch-then-insert wiring without spinning a real RSS driver.
	t.Parallel()
	articles := &fakeArticleStore{}
	ctrl := newCtrl(&fakeSourceStore{}, articles, nil)

	src := &v1.Source{ID: "x", Type: "fake"}
	out := []*v1.Article{{ID: "art-1"}, {ID: "art-2"}}
	ctrl.driver.RegisterRetriever(&fakeRetriever{kind: "fake", out: out})

	if err := ctrl.Run(t.Context(), src); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(articles.insertCalls) != 1 || len(articles.insertCalls[0]) != 2 {
		t.Errorf("articles inserted = %+v, want one batch of 2", articles.insertCalls)
	}
}

func TestRun_FetchError_Bubbles(t *testing.T) {
	t.Parallel()
	articles := &fakeArticleStore{}
	ctrl := newCtrl(&fakeSourceStore{}, articles, nil)
	ctrl.driver.RegisterRetriever(&fakeRetriever{kind: "fake", err: errors.New("404")})

	err := ctrl.Run(t.Context(), &v1.Source{Type: "fake"})
	if err == nil {
		t.Fatal("expected fetch error")
	}
	if len(articles.insertCalls) != 0 {
		t.Errorf("articles must not be inserted on fetch error, got %+v", articles.insertCalls)
	}
}

func TestRun_EmptyResult_NoInsert(t *testing.T) {
	t.Parallel()
	articles := &fakeArticleStore{}
	ctrl := newCtrl(&fakeSourceStore{}, articles, nil)
	ctrl.driver.RegisterRetriever(&fakeRetriever{kind: "fake", out: nil})

	if err := ctrl.Run(t.Context(), &v1.Source{Type: "fake"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(articles.insertCalls) != 0 {
		t.Errorf("empty fetch must not insert, got %+v", articles.insertCalls)
	}
}

func TestRun_UnknownType_Errors(t *testing.T) {
	t.Parallel()
	ctrl := newCtrl(&fakeSourceStore{}, &fakeArticleStore{}, nil)
	if err := ctrl.Run(t.Context(), &v1.Source{Type: "ghost"}); err == nil {
		t.Error("expected error for unknown retriever")
	}
}

func TestRunAll_IteratesActiveSourcesByType(t *testing.T) {
	t.Parallel()
	articles := &fakeArticleStore{}
	store := &fakeSourceStore{byType: map[v1.SourceType][]*v1.Source{
		v1.RSSType: {{ID: "a", Type: v1.RSSType}, {ID: "b", Type: v1.RSSType}},
	}}
	ctrl := newCtrl(store, articles, nil)
	ctrl.driver.RegisterRetriever(&fakeRetriever{kind: v1.RSSType, out: []*v1.Article{{ID: "x"}}})

	if err := ctrl.RunRSS(t.Context()); err != nil {
		t.Fatalf("RunRSS: %v", err)
	}
	if len(articles.insertCalls) != 2 {
		t.Errorf("expected 2 insert batches (one per source), got %d", len(articles.insertCalls))
	}
}

func TestSchedule_SpecOverridesDefault(t *testing.T) {
	t.Parallel()
	ctrl := newCtrl(&fakeSourceStore{}, &fakeArticleStore{}, nil)

	cases := []struct {
		name string
		src  *v1.Source
		want string
	}{
		{name: "rss explicit", src: &v1.Source{Type: v1.RSSType, Spec: &v1.RSSSpec{Schedule: "*/1 * * * *"}}, want: "*/1 * * * *"},
		{name: "rss default", src: &v1.Source{Type: v1.RSSType, Spec: &v1.RSSSpec{}}, want: defaultRssSchedule},
		{name: "http explicit", src: &v1.Source{Type: v1.HTTPType, Spec: &v1.HTTPSpec{Schedule: "0 * * * *"}}, want: "0 * * * *"},
		{name: "http default", src: &v1.Source{Type: v1.HTTPType, Spec: &v1.HTTPSpec{}}, want: defaultHttpSchedule},
		{name: "webhook falls to rss-default", src: &v1.Source{Type: v1.WebhookType, Spec: &v1.WebhookSpec{}}, want: defaultRssSchedule},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ctrl.Schedule(tc.src); got != tc.want {
				t.Errorf("Schedule = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTimeout_HonorsSourceOverride(t *testing.T) {
	t.Parallel()
	ctrl := newCtrl(&fakeSourceStore{}, &fakeArticleStore{}, nil)

	five := 5
	zero := 0
	neg := -1
	cases := []struct {
		name string
		src  *v1.Source
		want time.Duration
	}{
		{name: "default when nil", src: &v1.Source{}, want: defaultTimeout},
		{name: "default when zero", src: &v1.Source{TimeoutSeconds: &zero}, want: defaultTimeout},
		{name: "default when negative", src: &v1.Source{TimeoutSeconds: &neg}, want: defaultTimeout},
		{name: "honors positive", src: &v1.Source{TimeoutSeconds: &five}, want: 5 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ctrl.Timeout(tc.src); got != tc.want {
				t.Errorf("Timeout = %v, want %v", got, tc.want)
			}
		})
	}
}

type fakeRetriever struct {
	kind v1.SourceType
	out  []*v1.Article
	err  error
}

func (f *fakeRetriever) Kind() v1.SourceType { return f.kind }
func (f *fakeRetriever) Fetch(context.Context, *v1.Source) ([]*v1.Article, error) {
	return f.out, f.err
}
