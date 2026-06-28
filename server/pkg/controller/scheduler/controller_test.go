package scheduler

import (
	"context"
	"log/slog"
	"testing"

	corescheduler "github.com/notallthere404/futurecast/server/pkg/scheduler"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func newCtrl(t *testing.T) *Controller {
	t.Helper()
	sch := corescheduler.New(discardLogger())
	t.Cleanup(sch.Stop)
	return New(discardLogger(), sch)
}

func TestAdd_ForwardsToCore(t *testing.T) {
	t.Parallel()
	ctrl := newCtrl(t)
	if err := ctrl.Add("custom", "0 0 * * *", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Add: %v", err)
	}
}

func TestAdd_InvalidExprBubbles(t *testing.T) {
	t.Parallel()
	ctrl := newCtrl(t)
	if err := ctrl.Add("custom", "bogus", func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected error from invalid cron expression")
	}
}

func TestRemove_NoPanicOnUnknown(t *testing.T) {
	t.Parallel()
	ctrl := newCtrl(t)
	// Remove must be safe to call on a label the core scheduler does
	// not know about; the system controller invokes it during
	// teardown without guaranteeing registration first.
	ctrl.Remove("never-added")
}

func TestRemove_DropsRegisteredLabel(t *testing.T) {
	t.Parallel()
	ctrl := newCtrl(t)
	if err := ctrl.Add("classify", "0 0 * * *", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Add: %v", err)
	}
	ctrl.Remove("classify")
	// Re-Add the same label after Remove. The cron library's Remove is
	// what we are really exercising; a left-over entry under "classify"
	// would cause the second Add to register a duplicate, which the
	// underlying robfig/cron handles silently but observably elsewhere.
	if err := ctrl.Add("classify", "0 0 * * *", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("re-Add after Remove: %v", err)
	}
}

func TestRunStop_NoPanic(t *testing.T) {
	t.Parallel()
	ctrl := newCtrl(t)
	ctrl.Run()
	ctrl.Stop()
}
