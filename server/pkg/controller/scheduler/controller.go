// Package scheduler is a thin wrapper over the core cron scheduler.
//
// The controller no longer persists job rows or keeps a hardcoded label
// switch. Source cadence comes from each source's own `schedule` field
// in the config; the classify loop is signal-driven by the inference
// controller, not cron-driven. The remaining surface is Run / Stop for
// HTTP callers plus the Add / Remove primitives the system controller
// uses to register per-source jobs at startup.
//
// A future job-status surface (replacing the old NextJob list) will
// land here when the dashboard's new "active jobs" view is designed.
package scheduler

import (
	"context"
	"log/slog"

	corescheduler "github.com/notallthere404/futurecast/server/pkg/scheduler"
)

// Controller wraps the core cron scheduler with controller-style
// logging and exposes the slim Add/Remove/Run/Stop surface other
// controllers depend on.
type Controller struct {
	log       *slog.Logger
	scheduler *corescheduler.Scheduler
}

// New wires the scheduler controller around an already-constructed
// core scheduler.
func New(log *slog.Logger, scheduler *corescheduler.Scheduler) *Controller {
	return &Controller{
		log:       log.With(slog.String("controller", "scheduler")),
		scheduler: scheduler,
	}
}

// Add forwards to the core scheduler. The system controller calls this
// once per active retriever source during loadSources.
func (c *Controller) Add(label, expr string, fn func(context.Context) error) error {
	return c.scheduler.Add(label, expr, fn)
}

// Run starts the underlying cron loop. cron.Start() is non-blocking and
// spawns its own goroutine per job (with internal recover), so no `go`
// wrapper is needed here.
func (c *Controller) Run() {
	c.scheduler.Run()
}

// Stop signals the cron loop to halt and waits for in-flight jobs to
// finish (see pkg/scheduler.Scheduler.Stop for the wait semantics).
func (c *Controller) Stop() {
	c.scheduler.Stop()
}

// Remove drops a registered job by its label. Safe to call on labels
// that were never registered (Stop / teardown paths invoke it without
// guaranteeing prior Add).
func (c *Controller) Remove(label string) {
	c.scheduler.Remove(label)
}
