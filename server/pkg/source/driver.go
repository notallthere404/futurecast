// Package source is the parent for per-type source drivers (rss, http,
// websocket, webhook). It defines the Retriever / Listener contracts
// and a Driver registry so callers dispatch on v1.SourceType instead
// of switch ladders.
package source

import (
	"context"
	"fmt"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

// Retriever is implemented by pull-style sources (RSS, HTTP). The
// source controller calls Fetch on every cron tick.
type Retriever interface {
	Kind() v1.SourceType
	Fetch(context.Context, *v1.Source) ([]*v1.Article, error)
}

// Listener is implemented by push-style sources (webhook, websocket).
// Start spawns the listener and writes incoming articles to the
// channel; Register / Deregister bind active sources.
type Listener interface {
	Kind() v1.SourceType
	Start(context.Context, chan<- *v1.Article) error
	Stop()
	Register(*v1.Source)
	Deregister(srcID string)
}

// Driver is the per-process registry pairing each v1.SourceType to its
// concrete Retriever or Listener. The source controller looks drivers
// up here at fetch time and at listener-spawn time.
type Driver struct {
	retrievers map[v1.SourceType]Retriever
	listeners  map[v1.SourceType]Listener
}

// NewDriver returns an empty Driver. Callers populate it via
// RegisterRetriever / RegisterListener during boot.
func NewDriver() *Driver {
	return &Driver{
		retrievers: make(map[v1.SourceType]Retriever),
		listeners:  make(map[v1.SourceType]Listener),
	}
}

// RegisterRetriever stores r under r.Kind(); later lookups via
// Retriever(kind) return it. Last writer wins.
func (d *Driver) RegisterRetriever(r Retriever) { d.retrievers[r.Kind()] = r }

// RegisterListener stores l under l.Kind(); later lookups via
// Listener(kind) return it. Last writer wins.
func (d *Driver) RegisterListener(l Listener) { d.listeners[l.Kind()] = l }

// Retriever returns the registered retriever for t, or an error when
// no driver was registered for that kind.
func (d *Driver) Retriever(t v1.SourceType) (Retriever, error) {
	r, ok := d.retrievers[t]
	if !ok {
		return nil, fmt.Errorf("source: no retriever for %q", t)
	}
	return r, nil
}

// Listener returns the registered listener for t, or an error when
// no driver was registered for that kind.
func (d *Driver) Listener(t v1.SourceType) (Listener, error) {
	l, ok := d.listeners[t]
	if !ok {
		return nil, fmt.Errorf("source: no listener for %q", t)
	}
	return l, nil
}

// Listeners returns every registered listener. Used at shutdown to
// fan a Stop across them all.
func (d *Driver) Listeners() []Listener {
	out := make([]Listener, 0, len(d.listeners))
	for _, l := range d.listeners {
		out = append(out, l)
	}
	return out
}
