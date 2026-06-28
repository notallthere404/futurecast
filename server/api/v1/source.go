package v1

import (
	"encoding/json"
	"time"
)

// SourceType discriminates the source kind. `page` is folded into
// `rss` with Target=link, so it has no constant of its own.
type SourceType string

const (
	RSSType       SourceType = "rss"
	WebhookType   SourceType = "webhook"
	HTTPType      SourceType = "http"
	WebsocketType SourceType = "websocket"
)

// SourceTarget names the RSS field used as article content.
type SourceTarget string

const (
	ContentTarget     SourceTarget = "content"
	DescriptionTarget SourceTarget = "description"
	LinkTarget        SourceTarget = "link"
)

// SelectorType names a page-scrape selector role (which part of the
// page each CSS selector list extracts).
type SelectorType string

const (
	Nav       SelectorType = "nav"
	Title     SelectorType = "title"
	Content   SelectorType = "content"
	Timestamp SelectorType = "timestamp"
)

// SelectorMap is the per-role CSS selector list a page scraper applies
// (one entry per SelectorType the source needs).
type SelectorMap map[SelectorType][]string

// Trust is the operator-assigned weighting for a source, used as a
// signal during downstream scoring (higher trust = more confidence in
// classifications derived from this source's articles).
type Trust string

const (
	Unknown Trust = "unknown"
	Low     Trust = "low"
	Medium  Trust = "medium"
	High    Trust = "high"
)

// Source polymorphic ingest source. Shared columns are typed and indexable
// in Postgres; the type-specific tail (`Spec`) is stored in the `spec` jsonb
// column and dispatched on `Type` at read time.
type Source struct {
	ID             string          `json:"id" db:"id"`
	Type           SourceType      `json:"type" db:"type"`
	Name           string          `json:"name" db:"name"`
	URL            string          `json:"url" db:"url"`
	Hash           []byte          `json:"hash" db:"hash"`
	Active         bool            `json:"active" db:"active"`
	Trust          Trust           `json:"trust" db:"trust"`
	Description    string          `json:"description" db:"description"`
	Tags           []string        `json:"tags" db:"tags"`
	DedupeKey      *string         `json:"dedupe_key,omitempty" db:"dedupe_key"`
	TimeoutSeconds *int            `json:"timeout_seconds,omitempty" db:"timeout_seconds"`
	Auth           *Auth           `json:"auth,omitempty" db:"auth"`
	Extract        *Extract        `json:"extract,omitempty" db:"extract"`
	Retry          *Retry          `json:"retry,omitempty" db:"retry"`
	Headers        Headers         `json:"headers,omitempty" db:"headers"`
	Spec           Spec            `json:"spec,omitempty" db:"-"`
	SpecRaw        json.RawMessage `json:"-" db:"spec"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      *time.Time      `json:"updated_at" db:"updated_at"`
}

// SourceScrape is the payload for the scraper. Includes per-source visited URLs.
type SourceScrape struct {
	Source
	Completed []string `json:"completed" db:"completed"`
}

// SourceURL is one row in source_urls, tracking the terminal state of
// every URL a scraper visits. Lets future fetches dedupe completed
// URLs, retry failures, and queue discovered links.
type SourceURL struct {
	SourceID string     `json:"source_id"`
	URL      string     `json:"url"`
	Type     ResultType `json:"type"`
	Error    string     `json:"error,omitempty"`
}

// UnmarshalJSON reads `type` first, then dispatches `spec` into the matching
// concrete Spec via UnmarshalSpec.
func (s *Source) UnmarshalJSON(data []byte) error {
	type alias Source
	var aux struct {
		*alias
		Spec json.RawMessage `json:"spec"`
	}
	aux.alias = (*alias)(s)
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.Spec) > 0 && string(aux.Spec) != "null" {
		spec, err := UnmarshalSpec(s.Type, aux.Spec)
		if err != nil {
			return err
		}
		s.Spec = spec
		s.SpecRaw = aux.Spec
	}
	return nil
}

// HydrateSpec decodes SpecRaw (set by the DB layer) into Spec. Repo layer
// scans the jsonb column into SpecRaw, callers invoke this once before use.
func (s *Source) HydrateSpec() error {
	if s.Spec != nil || len(s.SpecRaw) == 0 {
		return nil
	}
	spec, err := UnmarshalSpec(s.Type, s.SpecRaw)
	if err != nil {
		return err
	}
	s.Spec = spec
	return nil
}

// RSS returns the concrete RSSSpec when Type=rss, else nil. Avoids
// repeating the `spec.(*RSSSpec)` type-assertion at every call site.
func (s *Source) RSS() *RSSSpec {
	spec, _ := s.Spec.(*RSSSpec)
	return spec
}

// HTTP returns the concrete HTTPSpec when Type=http, else nil.
func (s *Source) HTTP() *HTTPSpec {
	spec, _ := s.Spec.(*HTTPSpec)
	return spec
}

// Webhook returns the concrete WebhookSpec when Type=webhook, else nil.
func (s *Source) Webhook() *WebhookSpec {
	spec, _ := s.Spec.(*WebhookSpec)
	return spec
}

// Websocket returns the concrete WebsocketSpec when Type=websocket, else nil.
func (s *Source) Websocket() *WebsocketSpec {
	spec, _ := s.Spec.(*WebsocketSpec)
	return spec
}
