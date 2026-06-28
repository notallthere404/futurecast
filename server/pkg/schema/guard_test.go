package schema

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGuard_WriterExcludesReaders(t *testing.T) {
	t.Parallel()
	g := New()

	g.Lock()

	var readerEntered atomic.Bool
	done := make(chan struct{})
	go func() {
		g.RLock()
		readerEntered.Store(true)
		g.RUnlock()
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	if readerEntered.Load() {
		t.Fatal("reader entered while writer holds the lock")
	}

	g.Unlock()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader did not proceed after writer released the lock")
	}
	if !readerEntered.Load() {
		t.Error("reader never observed the unlock")
	}
}

func TestGuard_ReadersConcurrent(t *testing.T) {
	t.Parallel()
	g := New()

	var wg sync.WaitGroup
	released := make(chan struct{})
	const n = 8
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			g.RLock()
			<-released
			g.RUnlock()
		}()
	}

	// All readers must be able to acquire RLock simultaneously; if
	// RLock serialised them we would never reach this point.
	time.Sleep(50 * time.Millisecond)
	close(released)
	wg.Wait()
}

func TestGuard_WriterWaitsForReaders(t *testing.T) {
	t.Parallel()
	g := New()
	g.RLock()

	var writerEntered atomic.Bool
	done := make(chan struct{})
	go func() {
		g.Lock()
		writerEntered.Store(true)
		g.Unlock()
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	if writerEntered.Load() {
		t.Fatal("writer entered while reader holds the lock")
	}

	g.RUnlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("writer did not proceed after reader released the lock")
	}
}
