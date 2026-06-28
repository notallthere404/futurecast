package scheduler

import (
	"context"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"
)

// JobMap pairs caller-facing labels with the cron entry IDs the
// underlying library assigns; lets Remove(label) look up the right
// entry to delete.
type JobMap map[string]cron.EntryID

// Scheduler wraps robfig/cron with label-keyed job registration so
// callers refer to jobs by name rather than by internal cron entry ID.
type Scheduler struct {
	log     *slog.Logger
	cron    *cron.Cron
	job_map JobMap
	mu      sync.RWMutex
}

// New returns an empty scheduler. Call Run to start the loop.
func New(log *slog.Logger) *Scheduler {
	return &Scheduler{
		cron:    cron.New(),
		log:     log.With(slog.String("mod", "scheduler")),
		job_map: make(map[string]cron.EntryID),
	}
}

// Run starts the cron loop. Non-blocking — the cron library spawns
// per-job goroutines internally.
func (sch *Scheduler) Run() {
	sch.cron.Start()

	sch.log.Info("scheduler started")
}

// StopHTTP stops the cron scheduler and waits for in-flight jobs to
// finish. Returning before in-flight jobs drain meant schema mutators
// could overlap with a still-running classify or source-fetch tick,
// re-introducing the FK-driven deadlock that pkg/schema guards against.
func (sch *Scheduler) Stop() {
	<-sch.cron.Stop().Done()

	sch.log.Info("scheduler stopped")
}

func (sch *Scheduler) Add(label, expr string, fn func(context.Context) error) error {
	id, err := sch.cron.AddFunc(expr, func() {
		ctx := context.Background()
		sch.log.Info("job started")
		if err := fn(ctx); err != nil {
			sch.log.Error("job failed", "error", err)
			return
		}
	})
	if err != nil {
		return err
	}

	sch.mu.Lock()
	sch.job_map[label] = id
	sch.mu.Unlock()

	sch.log.Info("job added", "cron", expr)

	return nil
}

// AddUntil schedules a job that removes itself when fn returns done=true.
func (sch *Scheduler) AddUntil(label, expr string, fn func(context.Context) (bool, error)) error {
	id, err := sch.cron.AddFunc(expr, func() {
		ctx := context.Background()
		sch.log.Info("job started", "label", label)
		done, err := fn(ctx)
		if err != nil {
			sch.log.Error("job failed", "error", err)
			return
		}
		if done {
			sch.Remove(label)
		}
	})
	if err != nil {
		return err
	}

	sch.mu.Lock()
	sch.job_map[label] = id
	sch.mu.Unlock()

	sch.log.Info("job added", "cron", expr)

	return nil
}

func (sch *Scheduler) Remove(label string) {
	sch.mu.Lock()
	id, ok := sch.job_map[label]
	if !ok {
		sch.log.Warn("could not find job to remove")
	}

	sch.cron.Remove(id)
	delete(sch.job_map, label)

	sch.mu.Unlock()
}
