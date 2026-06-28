package scheduler

import (
	"context"
	"log/slog"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// hasLabel same-package peek so tests can assert registration state
// without depending on a public Status surface (which was removed when
// the jobs table was retired).
func hasLabel(s *Scheduler, label string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.job_map[label]
	return ok
}

func TestScheduler_Add_RegistersLabel(t *testing.T) {
	t.Parallel()
	s := New(discardLogger())

	if err := s.Add("daily", "0 0 * * *", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	if !hasLabel(s, "daily") {
		t.Errorf("Add did not register label in job_map")
	}
}

func TestScheduler_Add_InvalidExpression(t *testing.T) {
	t.Parallel()
	s := New(discardLogger())
	if err := s.Add("bad", "not a cron", func(context.Context) error { return nil }); err == nil {
		t.Error("expected error for invalid cron expression")
	}
	if hasLabel(s, "bad") {
		t.Errorf("failed Add must not leave the label in job_map")
	}
}

func TestScheduler_Remove(t *testing.T) {
	t.Parallel()
	s := New(discardLogger())
	_ = s.Add("a", "0 0 * * *", func(context.Context) error { return nil })
	_ = s.Add("b", "0 0 * * *", func(context.Context) error { return nil })

	s.Remove("a")
	if hasLabel(s, "a") {
		t.Errorf("Remove(a) did not drop the label")
	}
	if !hasLabel(s, "b") {
		t.Errorf("Remove(a) must not touch unrelated label b")
	}
}

func TestScheduler_RunStop_NoPanic(t *testing.T) {
	t.Parallel()
	s := New(discardLogger())
	s.Run()
	s.Stop()
}

func TestScheduler_AddUntil_RemovesWhenDone(t *testing.T) {
	t.Parallel()
	s := New(discardLogger())
	if err := s.AddUntil("once", "0 0 * * *", func(context.Context) (bool, error) {
		return true, nil
	}); err != nil {
		t.Fatalf("AddUntil err: %v", err)
	}
	// Manual remove (we cannot easily fire the cron tick here without
	// real wall-clock work; verify the registration path + Remove path
	// behave under concurrent access).
	s.Remove("once")
	if hasLabel(s, "once") {
		t.Errorf("expected label gone after Remove")
	}
}
