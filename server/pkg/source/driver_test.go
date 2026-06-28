package source

import (
	"context"
	"testing"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

type fakeRetriever struct{ kind v1.SourceType }

func (f *fakeRetriever) Kind() v1.SourceType { return f.kind }
func (f *fakeRetriever) Fetch(context.Context, *v1.Source) ([]*v1.Article, error) {
	return nil, nil
}

type fakeListener struct{ kind v1.SourceType }

func (f *fakeListener) Kind() v1.SourceType                             { return f.kind }
func (f *fakeListener) Start(context.Context, chan<- *v1.Article) error { return nil }
func (f *fakeListener) Stop()                                           {}
func (f *fakeListener) Register(*v1.Source)                             {}
func (f *fakeListener) Deregister(string)                               {}

func TestDriver_RetrieverLookup(t *testing.T) {
	t.Parallel()
	d := NewDriver()
	r := &fakeRetriever{kind: v1.RSSType}
	d.RegisterRetriever(r)

	got, err := d.Retriever(v1.RSSType)
	if err != nil {
		t.Fatalf("Retriever err: %v", err)
	}
	if got != r {
		t.Errorf("Retriever returned %v, want %v", got, r)
	}
}

func TestDriver_RetrieverNotFound(t *testing.T) {
	t.Parallel()
	d := NewDriver()
	if _, err := d.Retriever(v1.RSSType); err == nil {
		t.Error("expected error for unknown retriever")
	}
}

func TestDriver_ListenerLookup(t *testing.T) {
	t.Parallel()
	d := NewDriver()
	l := &fakeListener{kind: v1.WebhookType}
	d.RegisterListener(l)

	got, err := d.Listener(v1.WebhookType)
	if err != nil {
		t.Fatalf("Listener err: %v", err)
	}
	if got != l {
		t.Errorf("Listener returned %v, want %v", got, l)
	}
}

func TestDriver_ListenerNotFound(t *testing.T) {
	t.Parallel()
	d := NewDriver()
	if _, err := d.Listener(v1.WebhookType); err == nil {
		t.Error("expected error for unknown listener")
	}
}

func TestDriver_Listeners_All(t *testing.T) {
	t.Parallel()
	d := NewDriver()
	d.RegisterListener(&fakeListener{kind: v1.WebhookType})
	d.RegisterListener(&fakeListener{kind: v1.WebsocketType})

	all := d.Listeners()
	if len(all) != 2 {
		t.Fatalf("Listeners() = %d entries, want 2", len(all))
	}
}

func TestDriver_RegisterOverwrites(t *testing.T) {
	t.Parallel()
	d := NewDriver()
	first := &fakeRetriever{kind: v1.RSSType}
	second := &fakeRetriever{kind: v1.RSSType}
	d.RegisterRetriever(first)
	d.RegisterRetriever(second)

	got, _ := d.Retriever(v1.RSSType)
	if got != second {
		t.Errorf("re-register should overwrite, got %v want %v", got, second)
	}
}
