package rss

import (
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func TestClean(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "strip simple tags", in: "<p>hi</p>", want: "hi"},
		{name: "strip nested tags", in: "<div><b>bold</b> text</div>", want: "bold text"},
		{name: "collapse whitespace", in: "a    b\t\tc", want: "a b c"},
		{name: "unescape entities", in: "Tom &amp; Jerry", want: "Tom & Jerry"},
		{name: "trim ends", in: "   hello   ", want: "hello"},
		{name: "empty", in: "", want: ""},
		{name: "tags only", in: "<br/><hr/>", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := clean(tc.in); got != tc.want {
				t.Errorf("clean(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSelectContent(t *testing.T) {
	t.Parallel()
	item := &gofeed.Item{
		Content:     "FULL_CONTENT",
		Description: "DESCRIPTION",
		Link:        "https://x/y",
	}

	cases := []struct {
		name string
		spec *v1.RSSSpec
		want string
	}{
		{name: "nil spec falls back to description", spec: nil, want: "DESCRIPTION"},
		{name: "content target", spec: &v1.RSSSpec{Target: v1.ContentTarget}, want: "FULL_CONTENT"},
		{name: "description target", spec: &v1.RSSSpec{Target: v1.DescriptionTarget}, want: "DESCRIPTION"},
		{name: "link target empty (page driver handles)", spec: &v1.RSSSpec{Target: v1.LinkTarget}, want: ""},
		{name: "unknown target empty", spec: &v1.RSSSpec{Target: "bogus"}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := selectContent(tc.spec, item); got != tc.want {
				t.Errorf("selectContent = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseItems(t *testing.T) {
	t.Parallel()
	pub := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	feed := &gofeed.Feed{
		Items: []*gofeed.Item{
			{
				Title:           "first",
				Link:            "https://example.com/1",
				Description:     "<p>hello</p>",
				PublishedParsed: &pub,
			},
			{
				Title:       "no link, skipped",
				Link:        "",
				Description: "ignored",
			},
			{
				Title:       "empty content after clean, skipped",
				Link:        "https://example.com/3",
				Description: "<br/>",
			},
			{
				Title:       "uses now when no PublishedParsed",
				Link:        "https://example.com/4",
				Description: "ok",
			},
		},
	}
	src := &v1.Source{ID: "src1", Spec: &v1.RSSSpec{Target: v1.DescriptionTarget}}

	before := time.Now()
	got := parseItems(feed, src)
	after := time.Now()

	if len(got) != 2 {
		t.Fatalf("got %d items, want 2", len(got))
	}
	if got[0].Title != "first" || got[0].Link != "https://example.com/1" || got[0].Content != "hello" {
		t.Errorf("item 0 unexpected: %+v", got[0])
	}
	if !got[0].Timestamp.Equal(pub) {
		t.Errorf("item 0 timestamp = %v, want %v", got[0].Timestamp, pub)
	}
	if got[0].SourceID != "src1" || got[0].SourceType != "rss" {
		t.Errorf("item 0 source fields: %+v", got[0])
	}
	if got[0].Processed {
		t.Errorf("Processed should default false")
	}

	if got[1].Title != "uses now when no PublishedParsed" {
		t.Errorf("item 1 = %+v", got[1])
	}
	if got[1].Timestamp.Before(before) || got[1].Timestamp.After(after) {
		t.Errorf("item 1 timestamp %v not within [%v, %v]", got[1].Timestamp, before, after)
	}
}

func TestParseItems_LinkTargetSkipsAll(t *testing.T) {
	t.Parallel()
	feed := &gofeed.Feed{
		Items: []*gofeed.Item{
			{Link: "https://x/1", Content: "c", Description: "d"},
		},
	}
	src := &v1.Source{Spec: &v1.RSSSpec{Target: v1.LinkTarget}}
	if got := parseItems(feed, src); len(got) != 0 {
		t.Errorf("Target=link must skip all (handled by scraper); got %d", len(got))
	}
}
