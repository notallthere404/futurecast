package system

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
	"github.com/notallthere404/futurecast/server/pkg/config"
	configcontroller "github.com/notallthere404/futurecast/server/pkg/controller/config"
	"github.com/notallthere404/futurecast/server/pkg/schema"
	"github.com/notallthere404/futurecast/server/pkg/utils"
)

// SourceStore is the slice of the source store the system controller
// uses during syncSources to diff config against the DB.
type SourceStore interface {
	SelectSourceAll(context.Context) ([]*v1.Source, error)
	DeleteSourceBatch(context.Context, []string) error
	UpsertSourceBatch(context.Context, []*v1.Source) error
}

// ClassificationStore is the slice of the classification store the
// system controller uses during syncTables to diff config against
// the live set of classification tables.
type ClassificationStore interface {
	SelectTableAll(context.Context) (map[string]struct{}, error)
	SelectDataKeyAll(context.Context, string) (map[string]struct{}, error)
	UpdateDataKeyAll(context.Context, string, []string, []string) error
	CreateTable(context.Context, string) error
	DropTable(context.Context, string) error
}

// MonitorStore is the slice of the monitor store the heartbeat uses
// for uptime tracking. Kept on the system controller surface because
// uptime queries also surface through the system routes.
type MonitorStore interface {
	UpsertUptimeEntry(context.Context, int) (int, error)
	SelectUptimeTotal(context.Context, string, string) (float64, error)
	SelectUptimeSegment(context.Context, v1.RateFormat) ([]float64, error)
}

// Scheduler is the slice of the scheduler controller the system
// controller needs at boot (registers per-source cron entries via
// Add, then starts the loop via Run).
type Scheduler interface {
	Add(label, expr string, fn func(context.Context) error) error
	Run()
	Stop()
}

// Source is the slice of the source controller the system controller
// drives at boot: Register binds drivers and returns the active set,
// Run + RunWebhook execute fetches, Schedule + Timeout supply the
// per-source cron expr and per-fetch timeout, SetFilters publishes the
// parsed filter list each source should evaluate against incoming
// articles.
type Source interface {
	Register(context.Context) ([]*v1.Source, error)
	Run(context.Context, *v1.Source) error
	RunWebhook(context.Context) error
	Schedule(*v1.Source) string
	Timeout(*v1.Source) time.Duration
	SetFilters(map[string][]config.Filter)
}

// Controller is the top-level boot container. Startup sequences
// the subsystems (sources, classification tables, scheduler, heartbeat)
// in the order they depend on each other; UpdateConfig re-runs Startup
// after a config write to absorb the new state.
type Controller struct {
	log             *slog.Logger
	ctx             context.Context
	config          *configcontroller.Controller
	sources         SourceStore
	source          Source
	classifications ClassificationStore
	monitor         MonitorStore
	scheduler       Scheduler
	heartbeat       *Heartbeat
	guard           *schema.Guard
}

// New wires the system controller. The caller is responsible for
// constructing the underlying stores and slices and passing the same
// schema.Guard the inference + classification controllers hold.
func New(log *slog.Logger, cfg *configcontroller.Controller, sources SourceStore, source Source, classifications ClassificationStore, monitor MonitorStore, scheduler Scheduler, guard *schema.Guard) *Controller {
	return &Controller{
		log:             log.With(slog.String("controller", "system")),
		config:          cfg,
		sources:         sources,
		source:          source,
		classifications: classifications,
		monitor:         monitor,
		scheduler:       scheduler,
		heartbeat:       NewHeartbeat(log, monitor),
		guard:           guard,
	}
}

// Startup ctx is the long-lived server ctx (signal-cancellable).
// Synchronous setup uses a derived 30s ctx; listener goroutines spawned
// from loadSources receive the parent ctx so they survive setup.
func (c *Controller) Startup(ctx context.Context) error {
	setupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	c.ctx = ctx
	c.log.Info("initializing")

	cfg := c.config.Get()

	sources, err := cfg.Source.Resolve()
	if err != nil {
		c.log.Error("failed to resolve sources from config", "error", err)
		return err
	}
	if err := c.syncSources(setupCtx, sources); err != nil {
		return err
	}

	// Push the parsed per-source filter list into the source
	// controller before the cron + listener loops fire. A malformed
	// filter blocks startup so a bad config doesn't quietly let
	// articles bypass filtering.
	filters, err := cfg.Source.ResolveFilters()
	if err != nil {
		c.log.Error("failed to resolve source filters", "error", err)
		return err
	}
	c.source.SetFilters(filters)
	if err := c.syncTables(setupCtx, cfg.Inference.ClassificationMap()); err != nil {
		return err
	}
	if err := c.loadSources(ctx); err != nil {
		return err
	}
	// Start the cron loop once the per-source entries have been
	// registered. Idempotent; Restart() calls Stop() first.
	c.scheduler.Run()
	if err := c.heartbeat.Start(ctx); err != nil {
		return err
	}

	c.log.Info("initialization complete")
	return nil
}

func (c *Controller) Restart() error {
	c.scheduler.Stop()
	return c.Startup(c.ctx)
}

// ClientConfig is a passthrough to the config controller for the dashboard
// config editor and label-filter dropdowns.
func (c *Controller) ClientConfig() (config.ClientConfig, error) {
	return c.config.ClientConfig()
}

// UpdateConfig writes the new YAML to disk, reloads, and re-runs
// Startup so source/table sync and scheduling reflect the new config.
// Once hot reload is wired the Restart() call here can shrink to a
// targeted re-sync.
func (c *Controller) UpdateConfig(raw string) error {
	if err := c.config.Write(raw); err != nil {
		return err
	}
	return c.Restart()
}

func (c *Controller) UptimeTotal(ctx context.Context, start, end string) (float64, error) {
	return c.monitor.SelectUptimeTotal(ctx, start, end)
}

func (c *Controller) UptimeSegment(ctx context.Context, format v1.RateFormat) ([]float64, error) {
	return c.monitor.SelectUptimeSegment(ctx, format)
}

// syncSources diffs the in-DB source set against the just-resolved
// config set. Removed-from-config sources are deleted; new or changed
// sources are upserted (hash compare).
func (c *Controller) syncSources(ctx context.Context, fresh []*v1.Source) error {
	c.log.Debug("syncing sources", "count", len(fresh))
	existing, err := c.sources.SelectSourceAll(ctx)
	if err != nil {
		return err
	}

	prev := make(map[string]*v1.Source, len(existing))
	for _, source := range existing {
		prev[source.URL] = source
	}

	var remove []string
	var upsert []*v1.Source
	for _, source := range fresh {
		old, has := prev[source.URL]
		if !has || !utils.CompareHash(source.Hash, old.Hash) {
			upsert = append(upsert, source)
		}
		delete(prev, source.URL)
	}

	for url := range prev {
		remove = append(remove, url)
	}

	if len(remove) > 0 {
		c.log.Debug("removing sources", "count", len(remove))
		if err := c.sources.DeleteSourceBatch(ctx, remove); err != nil {
			return err
		}
	}

	if len(upsert) > 0 {
		c.log.Debug("upserting sources", "count", len(upsert))
		if err := c.sources.UpsertSourceBatch(ctx, upsert); err != nil {
			return err
		}
	}

	return nil
}

// syncTables diffs the in-DB classification tables against the
// classification-to-attribute-names map derived from the config.
//
// Held under guard.Lock so concurrent classifier inserts and dashboard
// reads can't race the CREATE/DROP statements; see pkg/schema doc for
// the FK-driven deadlock this prevents.
func (c *Controller) syncTables(ctx context.Context, classifications map[string][]string) error {
	c.guard.Lock()
	defer c.guard.Unlock()

	existing, err := c.classifications.SelectTableAll(ctx)
	if err != nil {
		return err
	}

	diff := make(map[string]int)
	for key := range classifications {
		if _, has := existing[key]; !has {
			diff[key] = 1
		} else {
			diff[key] = 0
			delete(existing, key)
		}
	}

	for remaining := range existing {
		diff[remaining] = -1
	}

	for table, change := range diff {
		if change == -1 {
			c.log.Debug("dropping table", "table", table)
			if err := c.classifications.DropTable(ctx, table); err != nil {
				return err
			}
			continue
		}

		newFields, ok := classifications[table]
		if !ok || len(newFields) == 0 {
			c.log.Error("invalid table: must have at least one field", "table", table)
			return errors.New("table must have at least one category")
		}

		if change == 1 {
			c.log.Debug("creating table", "table", table)
			if err := c.classifications.CreateTable(ctx, table); err != nil {
				return err
			}
			continue
		}

		prevFields, err := c.classifications.SelectDataKeyAll(ctx, table)
		if err != nil {
			return err
		}

		var add []string
		var remove []string
		for _, field := range newFields {
			if _, has := prevFields[field]; !has {
				add = append(add, field)
			} else {
				delete(prevFields, field)
			}
		}
		for prev := range prevFields {
			remove = append(remove, prev)
		}

		if len(add) > 0 || len(remove) > 0 {
			c.log.Debug("updating field(s)", "table", table)
			if err := c.classifications.UpdateDataKeyAll(ctx, table, add, remove); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadSources after sync, bind active sources to their runtime.
// Source.Register handles driver init + listener binding; we walk the
// returned active set to schedule retrievers and spawn listener drainers.
func (c *Controller) loadSources(ctx context.Context) error {
	active, err := c.source.Register(ctx)
	if err != nil {
		return err
	}

	hasListener := make(map[v1.SourceType]bool)
	for _, src := range active {
		switch src.Type {
		case v1.RSSType, v1.HTTPType:
			label := fmt.Sprintf("%s:%s", src.Type, src.ID)
			if err := c.scheduler.Add(label, c.source.Schedule(src), func(ctx context.Context) error {
				fc, cancel := context.WithTimeout(ctx, c.source.Timeout(src))
				defer cancel()
				return c.source.Run(fc, src)
			}); err != nil {
				c.log.Error("schedule source failed", "id", src.ID, "error", err)
			}
		case v1.WebhookType:
			hasListener[v1.WebhookType] = true
		}
	}

	if hasListener[v1.WebhookType] {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					c.log.Error("webhook listener panic", "panic", r, "stack", string(debug.Stack()))
				}
			}()
			if err := c.source.RunWebhook(ctx); err != nil && !errors.Is(err, context.Canceled) {
				c.log.Error("webhook listener exited", "error", err)
			}
		}()
	}

	return nil
}
