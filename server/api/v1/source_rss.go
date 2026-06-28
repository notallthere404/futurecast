package v1

// RSSSpec connector config for `type=rss`. When Target=link the linked page
// is fetched and Selectors apply; otherwise the feed field named by Target is
// used directly as article content.
type RSSSpec struct {
	URL       string       `json:"url"`
	Target    SourceTarget `json:"target,omitempty"`
	Limit     int          `json:"limit,omitempty"`
	Schedule  string       `json:"schedule,omitempty"`
	Paths     []string     `json:"paths,omitempty"`
	Selectors SelectorMap  `json:"selectors,omitempty"`
}

func (s *RSSSpec) Kind() SourceType { return RSSType }
