// Package inference owns the inference subsystem from the controller
// side: it composes a Client (pkg/inference) with a Mode and a
// Container into a level-triggered loop that classifies unprocessed
// articles. Sources Kick the controller after every insert; the loop
// drains until refill returns empty, then exits.
//
// Other controllers (source, classification) depend on this controller
// rather than on pkg/inference directly. This keeps the queue + mode
// orchestration in one place and lets the implementation swap
// (transformers to llama.cpp, self-hosted to api) without touching
// downstream code.
package inference

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/notallthere404/futurecast/server/pkg/inference"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
	cfgpkg "github.com/notallthere404/futurecast/server/pkg/config"
)

// Client is the runtime classifier surface. Declared as an interface
// here (not pulled from pkg/inference) so tests can drop in a fake
// without spinning the real HTTP transport. The production
// implementation is *pkg/inference.Client.
type Client interface {
	SetTarget(inference.Target)
	Target() inference.Target
	Info(ctx context.Context) (v1.InferenceInfo, error)
	Load(ctx context.Context, model, typ string) error
	Classify(ctx context.Context, art *v1.ClassifyArticle, spec []v1.ClassificationSpec) ([]*inference.ClassifyResponse, error)
}

// Container is the docker-compose lifecycle surface. Tests substitute
// a fake to avoid spawning real compose calls.
type Container interface {
	Stop(context.Context) error
	WaitForHealth(ctx context.Context, addr string) error
}

// Config is the slice of the config controller this controller
// observes. It hands out config snapshots and subscribes to reloads.
type Config interface {
	Get() *cfgpkg.Config
	OnReload(func(*cfgpkg.Config))
}

// Controller composes Client + Mode + Container with a small
// level-triggered loop. Sources call Kick after every insert; the
// loop drains the configured Mode until Refill returns empty, then
// exits. ready gates Kick until the inference backend has been
// confirmed reachable (or, for api mode, until the target is set).
type Controller struct {
	log       *slog.Logger
	config    Config
	client    Client
	container Container
	mode      inference.Mode

	lastConfig *cfgpkg.Config
	ready      atomic.Bool

	// loop state
	limit     int
	mu        sync.Mutex
	isRunning bool
	queue     []*v1.ClassifyArticle
	results   v1.MappedClassArray
	ctx       context.Context
}

// New wires the controller. mode picks the article-fetching policy:
// pass a ContinuousMode for the steady-state background loop, or a
// ManualMode if the deployment classifies on demand via the route
// handler.
func New(
	log *slog.Logger,
	cfg Config,
	client Client,
	container Container,
	mode inference.Mode,
) *Controller {
	c := &Controller{
		log:        log.With(slog.String("controller", "inference")),
		config:     cfg,
		client:     client,
		container:  container,
		mode:       mode,
		limit:      10,
		results:    make(v1.MappedClassArray),
		ctx:        context.Background(),
		lastConfig: cfg.Get(),
	}
	cfg.OnReload(c.onConfigReload)
	return c
}

// Start spawns the readiness goroutine. Kick is a no-op until the
// readiness probe completes (and for AutoDrive modes, fires once
// after to drain any backlog). Non-blocking — the dashboard and
// config endpoints serve immediately while the inference container
// pulls + loads a model.
func (c *Controller) Start(ctx context.Context) {
	c.mu.Lock()
	c.ctx = ctx
	c.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.log.Error("inference readiness panic", "panic", r, "stack", string(debug.Stack()))
			}
		}()
		if err := c.syncToConfig(ctx); err != nil {
			c.log.Error("inference readiness aborted", "error", err)
			return
		}
		c.ready.Store(true)
		c.log.Info("inference ready")
		if c.mode.AutoDrive() {
			c.startLoop()
		}
	}()
}

// Kick spawns the loop if it isn't already running, the inference
// service has passed its readiness check, and the active mode wants
// background drives. Sources call this after a successful article
// insert; the manual classify route uses ClassifyInline instead.
func (c *Controller) Kick() {
	if !c.ready.Load() {
		c.log.Debug("kick ignored: inference not ready")
		return
	}
	if !c.mode.AutoDrive() {
		c.log.Debug("kick ignored: mode is manual")
		return
	}
	c.startLoop()
}

// Stop brings the inference container down. Use when transitioning
// to type=api so the container does not stay running unused.
func (c *Controller) Stop(ctx context.Context) error {
	return c.container.Stop(ctx)
}

// ClassifyInline runs a one-shot classification of caller-supplied
// articles, persists the results via the active Mode, and returns
// the parsed responses. The manual classify route is the only
// expected caller; in continuous deployments it's still safe to use
// for ad-hoc classification of test articles.
func (c *Controller) ClassifyInline(ctx context.Context, arts []*v1.ClassifyArticle) ([]*inference.ClassifyResponse, error) {
	spec := c.config.Get().InferenceSpec()
	if len(spec) == 0 {
		return nil, errors.New("no classification spec configured")
	}
	out := make([]*inference.ClassifyResponse, 0, len(arts))
	acc := make(v1.MappedClassArray)
	for _, art := range arts {
		responses, err := c.client.Classify(ctx, art, spec)
		if err != nil {
			return nil, fmt.Errorf("classify %s: %w", art.ID, err)
		}
		out = append(out, responses...)
		parsed, err := inference.ParseClassifyResponses(responses)
		if err != nil {
			return nil, fmt.Errorf("parse responses for %s: %w", art.ID, err)
		}
		for name, classes := range parsed {
			acc[name] = append(acc[name], classes...)
		}
	}
	if err := c.mode.Persist(ctx, acc); err != nil {
		return nil, fmt.Errorf("persist: %w", err)
	}
	return out, nil
}

// ────────────────────────── readiness sync ──────────────────────────//

// syncToConfig pushes the dispatch target into the client and (for
// self-hosted modes) waits for the Python service to be reachable
// and serving the configured model. api mode skips the network probe
// entirely — failures surface as per-article classify errors and the
// next Kick retries naturally.
func (c *Controller) syncToConfig(ctx context.Context) error {
	cfg := c.config.Get()
	wantType := string(cfg.Inference.Type)
	wantModel := cfg.Inference.Model

	target := inference.Target{
		Type:  wantType,
		Addr:  cfg.Inference.Addr,
		Model: wantModel,
	}
	if wantType == "api" && cfg.Inference.Api != nil {
		target.Endpoint = cfg.Inference.Api.Endpoint
		target.APIKey = cfg.Inference.Api.APIKey
	}
	c.client.SetTarget(target)

	if wantType == "api" {
		c.log.Info("inference target=api; skipping self-hosted readiness probe",
			"endpoint", target.Endpoint, "model", wantModel)
		return nil
	}

	addr := cfg.Inference.Addr
	c.log.Info("waiting for inference", "addr", addr)
	if err := c.container.WaitForHealth(ctx, addr); err != nil {
		return fmt.Errorf("initial health wait: %w", err)
	}

	info, err := c.client.Info(ctx)
	if err != nil {
		return fmt.Errorf("query inference info: %w", err)
	}

	if info.Type == wantType && info.Model == wantModel {
		c.log.Info("inference matches config", "type", info.Type, "model", info.Model)
		return nil
	}

	c.log.Info("inference model mismatch; requesting load",
		"have_type", info.Type, "have_model", info.Model,
		"want_type", wantType, "want_model", wantModel)
	if err := c.client.Load(ctx, wantModel, wantType); err != nil {
		return fmt.Errorf("request load: %w", err)
	}
	if err := c.container.WaitForHealth(ctx, addr); err != nil {
		return fmt.Errorf("post-load health wait: %w", err)
	}
	c.log.Info("inference loaded", "type", wantType, "model", wantModel)
	return nil
}

// onConfigReload fires after a successful config write. If the
// inference dispatch target changed, swap it and (for self-hosted
// modes) wait for the new model to come live before resuming.
func (c *Controller) onConfigReload(cfg *cfgpkg.Config) {
	if !inference.InferenceChanged(c.lastConfig, cfg) {
		return
	}
	c.log.Info("inference config changed; reloading",
		"model", cfg.Inference.Model,
		"type", cfg.Inference.Type)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.log.Error("inference reload panic", "panic", r, "stack", string(debug.Stack()))
			}
		}()
		c.ready.Store(false)
		ctx := context.Background()
		if err := c.syncToConfig(ctx); err != nil {
			c.log.Error("inference reload failed", "error", err)
			return
		}
		c.ready.Store(true)
		c.lastConfig = cfg
		if c.mode.AutoDrive() {
			c.startLoop()
		}
	}()
}

// ────────────────────────── event loop ──────────────────────────//

// startLoop spawns the drain goroutine if it isn't already running.
// Idempotent and cheap — concurrent calls collapse into one.
func (c *Controller) startLoop() {
	c.mu.Lock()
	if c.isRunning {
		c.mu.Unlock()
		return
	}
	c.isRunning = true
	c.mu.Unlock()
	go c.loop()
}

// loop drives one drain-classify-persist-refill cycle until the mode
// stops yielding articles. Exit conditions: refill returns empty,
// refill errors, persist errors, or the worker context is cancelled.
func (c *Controller) loop() {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("inference loop panic", "panic", r, "stack", string(debug.Stack()))
		}
		c.mu.Lock()
		c.isRunning = false
		c.mu.Unlock()
	}()

	for {
		// Drain whatever is currently in the queue.
		for {
			c.mu.Lock()
			if len(c.queue) == 0 {
				c.mu.Unlock()
				break
			}
			next := c.queue[0]
			c.queue = c.queue[1:]
			ctx := c.ctx
			c.mu.Unlock()

			c.log.Debug("classifying next article", "id", next.ID)
			spec := c.config.Get().InferenceSpec()
			responses, err := c.client.Classify(ctx, next, spec)
			if err != nil {
				c.log.Error("classify failed", "article", next.ID, "error", err)
				continue
			}

			parsed, err := inference.ParseClassifyResponses(responses)
			if err != nil {
				c.log.Error("parse responses failed", "article", next.ID, "error", err)
				continue
			}

			c.mu.Lock()
			for name, classes := range parsed {
				c.results[name] = append(c.results[name], classes...)
			}
			c.mu.Unlock()
		}

		// Queue drained: snapshot results, persist, then refill.
		c.mu.Lock()
		results := c.results
		c.results = make(v1.MappedClassArray)
		ctx := c.ctx
		c.mu.Unlock()

		if ctx.Err() != nil {
			return
		}

		if len(results) > 0 {
			if err := c.mode.Persist(ctx, results); err != nil {
				c.log.Error("persist results failed", "error", err)
				return
			}
		}

		batch, err := c.mode.Refill(ctx, c.limit)
		if err != nil {
			c.log.Error("refill failed", "error", err)
			return
		}
		if len(batch) == 0 {
			return
		}

		c.mu.Lock()
		c.queue = append(c.queue, batch...)
		c.mu.Unlock()
	}
}

// stopClock is a small helper for tests that need to wait until the
// loop transitions to not-running.
func (c *Controller) loopRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isRunning
}

var _ = time.Second // keep time import in case future tweaks need it
