package v1

// HTTPSpec connector config for `type=http`. Scheduled HTTP requester.
type HTTPSpec struct {
	Method     string            `json:"method,omitempty"`
	Schedule   string            `json:"schedule,omitempty"`
	Query      map[string]string `json:"query,omitempty"`
	Body       string            `json:"body,omitempty"`
	Pagination *Pagination       `json:"pagination,omitempty"`
	State      *State            `json:"state,omitempty"`
}

func (s *HTTPSpec) Kind() SourceType { return HTTPType }

// Pagination cursor / next-page handling for HTTP responses.
type Pagination struct {
	CursorPath  string `json:"cursor_path,omitempty"`
	NextUrlPath string `json:"next_url_path,omitempty"`
	PageParam   string `json:"page_param,omitempty"`
	MaxPages    int    `json:"max_pages,omitempty"`
}

// State incremental polling cursor. Driver persists between runs.
type State struct {
	Kind  string `json:"kind"` // "last_success" | "since" | "etag"
	Value string `json:"value,omitempty"`
}
