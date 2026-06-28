package system

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

type heartbeatStore interface {
	UpsertUptimeEntry(context.Context, int) (int, error)
}

// Heartbeat ticks once per checkInterval and upserts a monitor_uptime
// row so the dashboard can render an outage timeline. Start spawns the
// loop, Stop cancels it; both are safe to call repeatedly.
type Heartbeat struct {
	log    *slog.Logger
	store  heartbeatStore
	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewHeartbeat builds a Heartbeat scoped to the given uptime store.
// The system controller constructs one during boot.
func NewHeartbeat(log *slog.Logger, store heartbeatStore) *Heartbeat {
	return &Heartbeat{
		log:   log.With(slog.String("controller", "heartbeat")),
		store: store,
	}
}

func (h *Heartbeat) Start(ctx context.Context) error {
	const (
		checkInterval    = 15 * time.Second
		thresholdSeconds = 30
	)

	h.Stop()

	if _, err := h.store.UpsertUptimeEntry(ctx, thresholdSeconds); err != nil {
		return err
	}
	h.log.Debug("internal uptime check completed")

	monitorCtx, cancel := context.WithCancel(context.Background())
	h.mu.Lock()
	h.cancel = cancel
	h.mu.Unlock()

	go h.run(monitorCtx, checkInterval, thresholdSeconds)
	return nil
}

func (h *Heartbeat) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.cancel == nil {
		return
	}

	h.cancel()
	h.cancel = nil
}

func (h *Heartbeat) run(ctx context.Context, interval time.Duration, threshold int) {
	defer func() {
		if r := recover(); r != nil {
			h.log.Error("heartbeat panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.log.Debug("internal uptime monitor stopped")
			return
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(ctx, interval)
			_, err := h.store.UpsertUptimeEntry(checkCtx, threshold)
			cancel()
			if err != nil {
				h.log.Error("internal uptime check failed", "error", err)
				continue
			}
			h.log.Debug("internal uptime check completed")
		}
	}
}
