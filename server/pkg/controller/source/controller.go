package source

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
	"github.com/notallthere404/futurecast/server/pkg/config"
	"github.com/notallthere404/futurecast/server/pkg/httpx"
	"github.com/notallthere404/futurecast/server/pkg/schema"
	"github.com/notallthere404/futurecast/server/pkg/source"
	httpsource "github.com/notallthere404/futurecast/server/pkg/source/http"
	rsssource "github.com/notallthere404/futurecast/server/pkg/source/rss"
	webhooksource "github.com/notallthere404/futurecast/server/pkg/source/webhook"
)

// Store is the source-storage surface: CRUD on the sources table.
type Store interface {
	SelectSourceAll(context.Context) ([]*v1.Source, error)
	SelectSourceByType(context.Context, v1.SourceType) ([]*v1.Source, error)
	UpsertSource(context.Context, *v1.Source) error
	UpsertSourceBatch(context.Context, []*v1.Source) error
}

// ArticleStore is the article-side surface the source controller needs:
// reads for the dashboard's recent + rate views, writes for the inserter.
type ArticleStore interface {
	SelectArticleRecent(context.Context) ([]*v1.Article, error)
	SelectArticleRate(context.Context, v1.RateFormat) ([]int, error)
	InsertArticleBatch(context.Context, []*v1.Article) error
}

// Inference is the slice of the inference controller that the source
// controller signals after every successful article insert. Declared as
// an interface so tests can drop in a fake kick recorder. The worker
// itself decides whether to do anything with the signal (no-op if
// already running, drain otherwise).
type Inference interface {
	Kick()
}

// Controller owns the source subsystem: CRUD, per-source fetch cycles,
// listener drainers, and the kick signal sent to the inference worker
// whenever new articles land. The filterRegistry holds the active
// per-source filter list, repopulated by the system controller after
// every config (re)load.
type Controller struct {
	log       *slog.Logger
	store     Store
	articles  ArticleStore
	client    *httpx.Client
	driver    *source.Driver
	guard     *schema.Guard
	inference Inference
	filters   *filterRegistry
	// sourcesByID lets the listener drainer recover the source the
	// incoming article belongs to (the driver tags art.SourceID before
	// posting). Populated by Register; used by runListener to dispatch
	// to insertOne with the right src so filters apply.
	sourcesMu   sync.RWMutex
	sourcesByID map[string]*v1.Source
}

// New wires the source controller. The inference slice receives a Kick
// after every successful article insert; pass a real inference
// controller in production or a recording fake in tests.
func New(log *slog.Logger, store Store, articles ArticleStore, client *httpx.Client, guard *schema.Guard, inference Inference) *Controller {
	return &Controller{
		log:         log.With(slog.String("controller", "source")),
		store:       store,
		articles:    articles,
		client:      client,
		driver:      source.NewDriver(),
		guard:       guard,
		inference:   inference,
		filters:     newFilterRegistry(),
		sourcesByID: make(map[string]*v1.Source),
	}
}

// SetFilters replaces the per-source filter list. Called by the
// system controller after parsing config so the active filters
// reflect the latest YAML on every (re)load.
func (c *Controller) SetFilters(m map[string][]config.Filter) {
	c.filters.Set(m)
}

// filterArticles drops articles that fail any of src's configured
// filters. Logs evaluation errors but does not propagate them: a
// malformed regex in one filter shouldn't fail the whole fetch.
func (c *Controller) filterArticles(src *v1.Source, arts []*v1.Article) []*v1.Article {
	fs := c.filters.Get(src.URL)
	out, errs := applyFilters(fs, arts)
	for _, err := range errs {
		c.log.Warn("filter eval failed", "source", src.Name, "error", err)
	}
	if dropped := len(arts) - len(out); dropped > 0 {
		c.log.Debug("articles filtered", "source", src.Name, "kept", len(out), "dropped", dropped)
	}
	return out
}

func (c *Controller) List(ctx context.Context) ([]*v1.Source, error) {
	return c.store.SelectSourceAll(ctx)
}

func (c *Controller) Upsert(ctx context.Context, src *v1.Source) error {
	return c.store.UpsertSource(ctx, src)
}

func (c *Controller) UpsertBatch(ctx context.Context, srcs []*v1.Source) error {
	return c.store.UpsertSourceBatch(ctx, srcs)
}

// Article-touching paths take RLock so they park if a config reload
// is mid-syncTables. CREATE/DROP on a classification table briefly
// needs an AccessExclusiveLock on `articles` (FK trigger), which
// would deadlock against a concurrent INSERT/SELECT here.

func (c *Controller) Recent(ctx context.Context) ([]*v1.Article, error) {
	c.guard.RLock()
	defer c.guard.RUnlock()
	return c.articles.SelectArticleRecent(ctx)
}

func (c *Controller) Rate(ctx context.Context, format v1.RateFormat) ([]int, error) {
	c.guard.RLock()
	defer c.guard.RUnlock()
	return c.articles.SelectArticleRate(ctx, format)
}

func (c *Controller) InsertBatch(ctx context.Context, articles []*v1.Article) error {
	// External InsertBatch (route handler) has no source context, so
	// no filters apply. Per-source filtering is handled inside Run /
	// insertOne where the *v1.Source is in scope.
	c.guard.RLock()
	if err := c.articles.InsertArticleBatch(ctx, articles); err != nil {
		c.guard.RUnlock()
		return err
	}
	c.guard.RUnlock()
	if len(articles) > 0 {
		c.inference.Kick()
	}
	return nil
}

// Register performs driver init + listener binds. Returns the active source
// set so system.loadSources can iterate once to schedule retrievers and
// spawn listener drainers without re-querying the store.
func (c *Controller) Register(ctx context.Context) ([]*v1.Source, error) {
	srcs, err := c.store.SelectSourceAll(ctx)
	if err != nil {
		return nil, err
	}

	active := make([]*v1.Source, 0, len(srcs))
	byID := make(map[string]*v1.Source, len(srcs))
	typesSeen := make(map[v1.SourceType]bool)
	for _, src := range srcs {
		if !src.Active {
			continue
		}
		active = append(active, src)
		byID[src.ID] = src

		if !typesSeen[src.Type] {
			c.registerDriver(src.Type)
			typesSeen[src.Type] = true
		}

		// Listener sources bind into their driver at register time.
		if l, err := c.driver.Listener(src.Type); err == nil {
			l.Register(src)
		}
	}

	c.sourcesMu.Lock()
	c.sourcesByID = byID
	c.sourcesMu.Unlock()

	return active, nil
}

func (c *Controller) registerDriver(t v1.SourceType) {
	switch t {
	case v1.RSSType:
		c.driver.RegisterRetriever(rsssource.New(c.log))
	case v1.HTTPType:
		c.driver.RegisterRetriever(httpsource.New(c.log, c.client))
	case v1.WebhookType:
		c.driver.RegisterListener(webhooksource.New(c.log))
	default:
		c.log.Warn("no driver for source type", "type", t)
	}
}

// Run performs a single source fetch + insert. Called by the cron closure built
// in system.loadSources (one closure per active retriever source).
func (c *Controller) Run(ctx context.Context, src *v1.Source) error {
	r, err := c.driver.Retriever(src.Type)
	if err != nil {
		return err
	}

	arts, err := r.Fetch(ctx, src)
	if err != nil {
		return fmt.Errorf("fetch %s %q: %w", src.Type, src.Name, err)
	}
	arts = c.filterArticles(src, arts)
	if len(arts) == 0 {
		return nil
	}
	c.guard.RLock()
	if err := c.articles.InsertArticleBatch(ctx, arts); err != nil {
		c.guard.RUnlock()
		return err
	}
	c.guard.RUnlock()
	c.inference.Kick()
	return nil
}

func (c *Controller) RunRSS(ctx context.Context) error  { return c.runAll(ctx, v1.RSSType) }
func (c *Controller) RunHTTP(ctx context.Context) error { return c.runAll(ctx, v1.HTTPType) }

func (c *Controller) runAll(ctx context.Context, t v1.SourceType) error {
	srcs, err := c.store.SelectSourceByType(ctx, t)
	if err != nil {
		return err
	}
	for _, src := range srcs {
		fc, cancel := context.WithTimeout(ctx, c.Timeout(src))
		if err := c.Run(fc, src); err != nil {
			c.log.Error("source run failed", "type", src.Type, "name", src.Name, "error", err)
		}
		cancel()
	}
	return nil
}

// RunWebhook drains the webhook listener's article channel into the
// article store. Spawned as a goroutine from system.loadSources; lives
// until ctx cancels.
func (c *Controller) RunWebhook(ctx context.Context) error {
	return c.runListener(ctx, v1.WebhookType)
}

func (c *Controller) runListener(ctx context.Context, t v1.SourceType) error {
	l, err := c.driver.Listener(t)
	if err != nil {
		c.log.Debug("no listener registered, skipping", "type", t)
		return nil //nolint:nilerr // missing listener is a no-op, not a failure
	}

	out := make(chan *v1.Article, 256)
	c.log.Info("starting listener", "type", t)
	if err := l.Start(ctx, out); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			l.Stop()
			return ctx.Err()
		case art := <-out:
			src := c.lookupSource(art.SourceID)
			if src == nil {
				c.log.Warn("listener insert: no source for article", "source_id", art.SourceID)
				continue
			}
			if err := c.insertOne(ctx, src, art); err != nil {
				c.log.Error("listener insert failed", "type", t, "error", err)
			}
		}
	}
}

// lookupSource returns the *v1.Source matching id, or nil when no
// active source is registered under that id. Read with RLock so
// concurrent reads (one per drain) don't contend.
func (c *Controller) lookupSource(id string) *v1.Source {
	c.sourcesMu.RLock()
	defer c.sourcesMu.RUnlock()
	return c.sourcesByID[id]
}

// insertOne is an RLock-wrapped single-article insert. Pulled out so the
// listener-drainer body stays readable while still holding the guard
// around the write. Kicks the inference worker after a successful
// insert so a webhook landing between cron ticks does not have to wait.
func (c *Controller) insertOne(ctx context.Context, src *v1.Source, art *v1.Article) error {
	arts := c.filterArticles(src, []*v1.Article{art})
	if len(arts) == 0 {
		return nil
	}
	c.guard.RLock()
	if err := c.articles.InsertArticleBatch(ctx, arts); err != nil {
		c.guard.RUnlock()
		return err
	}
	c.guard.RUnlock()
	c.inference.Kick()
	return nil
}

// WebhookHandler exposes the webhook listener as an http.Handler for
// the server to mount at /webhooks/. Returns nil if no webhook listener
// is registered (so the server can `if wh != nil { mount }` cleanly).
func (c *Controller) WebhookHandler() http.Handler {
	l, err := c.driver.Listener(v1.WebhookType)
	if err != nil {
		return nil
	}
	wh, ok := l.(*webhooksource.Webhook)
	if !ok || wh == nil {
		return nil
	}
	return wh
}

const (
	defaultRssSchedule  = "*/10 * * * *"
	defaultHttpSchedule = "*/5 * * * *"
	defaultTimeout      = 30 * time.Second
)

// Schedule returns the cron expression for a source: honors Spec.Schedule
// if set, else falls back to per-type defaults.
func (c *Controller) Schedule(src *v1.Source) string {
	switch s := src.Spec.(type) {
	case *v1.RSSSpec:
		if s.Schedule != "" {
			return s.Schedule
		}
	case *v1.HTTPSpec:
		if s.Schedule != "" {
			return s.Schedule
		}
	}
	switch src.Type {
	case v1.HTTPType:
		return defaultHttpSchedule
	default:
		return defaultRssSchedule
	}
}

// Timeout returns the per-fetch timeout: honors src.TimeoutSeconds, else default.
func (c *Controller) Timeout(src *v1.Source) time.Duration {
	if src.TimeoutSeconds != nil && *src.TimeoutSeconds > 0 {
		return time.Duration(*src.TimeoutSeconds) * time.Second
	}
	return defaultTimeout
}
