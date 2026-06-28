package v1

import (
	"time"

	"github.com/notallthere404/futurecast/server/pkg/utils"
)

// Article is the canonical ingested-content row in the articles table.
// Processed flips true once the inference worker has handled the row.
type Article struct {
	ID         string     `json:"id" db:"id"`
	SourceID   string     `json:"source_id" db:"source_id"`
	SourceType SourceType `json:"source_type" db:"source_type"`
	Title      string     `json:"title" db:"title"`
	Content    string     `json:"content" db:"content"`
	Timestamp  time.Time  `json:"timestamp" db:"timestamp"`
	Link       string     `json:"link" db:"link"`
	Processed  bool       `json:"processed" db:"processed"`
}

// ClassifyArticle is the slim shape posted to the inference service:
// only the fields the classifier reads.
type ClassifyArticle struct {
	ID        string    `json:"id" db:"id"`
	Content   string    `json:"content" db:"content"`
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
}

// ResultType discriminates the source_urls row a fetch attempt produces:
// successful (completed), transient failure (retry), or newly-discovered
// URL queued for later fetch (discover).
type ResultType string

const (
	Completed ResultType = "completed"
	Retried   ResultType = "retry"
	Discover  ResultType = "discover"
)

// Result is the raw scraper envelope, split into (Article, SourceURL)
// by ParseResult before the stores accept it.
type Result struct {
	Type      ResultType
	ID        string
	URL       string
	Title     string
	Content   string
	Timestamp string
	Reason    string
}

// ParseResult splits a scraper Result into its persisted rows. Returns
// (nil, srcURL) for errors and empty-title placeholders — no article
// row but the URL still records terminal state for dedupe.
func (res *Result) ParseResult() (*Article, *SourceURL) {
	srcUrl := &SourceURL{
		SourceID: res.ID,
		URL:      res.URL,
		Type:     res.Type,
		Error:    res.Reason,
	}

	if res.Type == "error" || res.Title != "" {
		return nil, srcUrl
	}

	var id string
	if res.URL == "" {
		id = utils.NewUuidv4()
	} else {
		id = utils.NewUUIDv5(res.URL)
	}

	timestamp, err := time.Parse(time.RFC3339, res.Timestamp)
	if err != nil {
		timestamp = time.Now()
	}

	article := &Article{
		ID:         id,
		SourceID:   res.ID,
		SourceType: "page",
		Title:      res.Title,
		Content:    res.Content,
		Timestamp:  timestamp,
		Link:       res.URL,
		Processed:  false,
	}

	return article, srcUrl
}

// RateFormat picks article-rate granularity. Day = hourly buckets over
// the last 24h; Month = daily buckets over the last 30d.
type RateFormat string

const (
	Day   RateFormat = "day"
	Month RateFormat = "month"
)

// ArticleRate is the article-rate response: ordered bucket counts whose
// meaning is fixed by the RateFormat the caller passed.
type ArticleRate struct {
	Values []int `json:"values"`
}
