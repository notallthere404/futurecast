// Package rss; driver for SourceType=rss. Fetches one feed per call;
// concurrency is the caller's concern (cron entries run in their own goroutines).
package rss

import (
	"context"
	"html"
	"log/slog"
	"regexp"
	"strings"
	"time"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
	"github.com/notallthere404/futurecast/server/pkg/utils"

	"github.com/mmcdole/gofeed"
)

// RSS implements source.Retriever for RSS / Atom feeds, fetching one
// feed per call via gofeed.
type RSS struct {
	log    *slog.Logger
	parser *gofeed.Parser
}

var (
	htmlPattern       = regexp.MustCompile(`<[^>]*>`)
	whitespacePattern = regexp.MustCompile(`\s{2,}`)
)

// New returns an RSS retriever with a fresh gofeed parser.
func New(log *slog.Logger) *RSS {
	return &RSS{
		log:    log.With(slog.String("source", "rss")),
		parser: gofeed.NewParser(),
	}
}

// Kind reports which SourceType this driver handles.
func (r *RSS) Kind() v1.SourceType { return v1.RSSType }

// Fetch parses one feed into articles. Sync; caller's
// goroutine (cron tick) blocks until done or ctx expires.
func (r *RSS) Fetch(ctx context.Context, src *v1.Source) ([]*v1.Article, error) {
	feed, err := r.parser.ParseURLWithContext(src.URL, ctx)
	if err != nil {
		return nil, err
	}
	if feed == nil || len(feed.Items) == 0 {
		r.log.Debug("feed empty or nil", "name", src.Name)
		return nil, nil
	}
	return parseItems(feed, src), nil
}

func parseItems(feed *gofeed.Feed, src *v1.Source) []*v1.Article {
	spec := src.RSS()
	items := make([]*v1.Article, 0, len(feed.Items))

	for _, item := range feed.Items {
		if item.Link == "" {
			continue
		}
		id := utils.NewUUIDv5(item.Link)

		ts := time.Now()
		if item.PublishedParsed != nil {
			ts = *item.PublishedParsed
		}

		content := selectContent(spec, item)
		content = clean(content)
		if content == "" {
			continue
		}

		items = append(items, &v1.Article{
			ID:         id,
			SourceID:   src.ID,
			SourceType: "rss",
			Title:      item.Title,
			Content:    content,
			Timestamp:  ts,
			Link:       item.Link,
			Processed:  false,
		})
	}
	return items
}

func selectContent(spec *v1.RSSSpec, item *gofeed.Item) string {
	if spec == nil {
		return item.Description
	}
	switch spec.Target {
	case v1.ContentTarget:
		return item.Content
	case v1.DescriptionTarget:
		return item.Description
	case v1.LinkTarget:
		// Link-target rss = page scrape; handled by scraper driver.
		return ""
	}
	return ""
}

func clean(s string) string {
	s = html.UnescapeString(s)
	s = htmlPattern.ReplaceAllString(s, "")
	s = whitespacePattern.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
