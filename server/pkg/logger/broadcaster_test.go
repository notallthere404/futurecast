package logger

import (
	"sync"
	"testing"
	"time"
)

func TestBroadcaster_SubscribeReceivesMessage(t *testing.T) {
	t.Parallel()
	b := NewBroadcaster()
	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	b.Send("hello")

	select {
	case got := <-ch:
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestBroadcaster_FanOut(t *testing.T) {
	t.Parallel()
	b := NewBroadcaster()
	subs := make([]chan string, 5)
	for i := range subs {
		subs[i] = b.Subscribe()
	}
	defer func() {
		for _, ch := range subs {
			b.Unsubscribe(ch)
		}
	}()

	b.Send("ping")

	for i, ch := range subs {
		select {
		case got := <-ch:
			if got != "ping" {
				t.Errorf("sub %d got %q, want %q", i, got, "ping")
			}
		case <-time.After(time.Second):
			t.Fatalf("sub %d timeout", i)
		}
	}
}

func TestBroadcaster_DropOnFullBuffer(t *testing.T) {
	t.Parallel()
	b := NewBroadcaster()
	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	// Buffer is 64; saturate + extra drops, no blocking.
	for i := range 200 {
		b.Send("msg")
		_ = i
	}

	// We should read at least one buffered message immediately.
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected at least one buffered message")
	}
}

func TestBroadcaster_UnsubscribeClosesChannel(t *testing.T) {
	t.Parallel()
	b := NewBroadcaster()
	ch := b.Subscribe()

	b.Unsubscribe(ch)

	if _, open := <-ch; open {
		t.Error("Unsubscribe should close channel")
	}

	// Send after Unsubscribe must not panic; the channel is no longer in map.
	b.Send("after-unsub")
}

func TestBroadcaster_ConcurrentSubscribers(t *testing.T) {
	t.Parallel()
	b := NewBroadcaster()
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := b.Subscribe()
			b.Unsubscribe(ch)
		}()
	}
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Send("x")
		}()
	}
	wg.Wait()
}
