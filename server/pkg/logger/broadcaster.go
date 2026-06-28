package logger

import "sync"

// Broadcaster fans log lines out to every subscribed channel. The
// dashboard's live-log endpoint subscribes one channel per SSE client
// and forwards messages to the browser.
type Broadcaster struct {
	mu      sync.RWMutex
	clients map[chan string]struct{}
}

// NewBroadcaster returns an empty Broadcaster ready for Subscribe.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		clients: make(map[chan string]struct{}),
	}
}

func (b *Broadcaster) Subscribe() chan string {
	// Create new channel for subscriber
	ch := make(chan string, 64)

	b.mu.Lock()
	// Channel is added to client map
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	return ch
}

func (b *Broadcaster) Unsubscribe(ch chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Channel is deleted from map and closed
	delete(b.clients, ch)
	close(ch)
}

func (b *Broadcaster) Send(msg string) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}
