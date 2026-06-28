package config

// SourceConfig `source:` block. Defaults live under a `default`
// sub-key; per-kind lists hold concrete sources.
//
// Resolution rule: child entry overrides default; lists/maps REPLACE
// (no merge). Empty/zero child fields keep the parent value.
type SourceConfig struct {
	Default SourceDefaults `mapstructure:"default"`

	// Per-kind instances
	RSS       []RSSConfig       `mapstructure:"rss"`
	HTTP      []HTTPConfig      `mapstructure:"http"`
	Webhook   []WebhookConfig   `mapstructure:"webhook"`
	Websocket []WebsocketConfig `mapstructure:"websocket"`
}

// SourceDefaults holds the fields under `source.default:` inherited by every
// source instance unless the instance overrides them.
type SourceDefaults struct {
	Schedule       string            `mapstructure:"schedule"`
	Infer          []string          `mapstructure:"infer"`
	Trust          string            `mapstructure:"trust"`
	Filter         []string          `mapstructure:"filter"`
	Retry          *Retry            `mapstructure:"retry"`
	Headers        map[string]string `mapstructure:"headers"`
	TimeoutSeconds int               `mapstructure:"timeout_seconds"`
}

// CommonFields holds the fields every source kind shares. Embedded so each
// kind keeps a flat shape while inheriting the defaults set.
type CommonFields struct {
	Name           string            `mapstructure:"name"`
	URL            string            `mapstructure:"url"`
	Active         bool              `mapstructure:"active"`
	Trust          string            `mapstructure:"trust"`
	Description    string            `mapstructure:"description"`
	Tags           []string          `mapstructure:"tags"`
	Schedule       string            `mapstructure:"schedule"`
	Infer          []string          `mapstructure:"infer"`
	Filter         []string          `mapstructure:"filter"`
	TimeoutSeconds int               `mapstructure:"timeout_seconds"`
	Headers        map[string]string `mapstructure:"headers"`
	Auth           *Auth             `mapstructure:"auth"`
	Extract        *Extract          `mapstructure:"extract"`
	Retry          *Retry            `mapstructure:"retry"`
}

// RSSConfig rss source. Target picks the feed field used as content
// ("description" | "content" | "link"). When Target=link the linked page
// is fetched and Selectors apply.
type RSSConfig struct {
	CommonFields `mapstructure:",squash"`
	Target       string              `mapstructure:"target"`
	Limit        int                 `mapstructure:"limit"`
	Paths        []string            `mapstructure:"paths"`
	Selectors    map[string][]string `mapstructure:"selectors"`
}

// HTTPConfig describes a scheduled JSON requester. Method defaults to GET.
type HTTPConfig struct {
	CommonFields `mapstructure:",squash"`
	Method       string            `mapstructure:"method"`
	Query        map[string]string `mapstructure:"query"`
	Body         string            `mapstructure:"body"`
	Pagination   *Pagination       `mapstructure:"pagination"`
}

// WebhookConfig inbound. Path is mounted on the public mux at boot.
type WebhookConfig struct {
	CommonFields        `mapstructure:",squash"`
	Path                string `mapstructure:"path"`
	Method              string `mapstructure:"method"`
	ContentType         string `mapstructure:"content_type"`
	MaxBodyBytes        int    `mapstructure:"max_body_bytes"`
	ReplayWindowSeconds int    `mapstructure:"replay_window_seconds"`
}

// WebsocketConfig long-running. Heartbeat + Reconnect default off.
type WebsocketConfig struct {
	CommonFields `mapstructure:",squash"`
	Protocols    []string   `mapstructure:"protocols"`
	Subscribe    string     `mapstructure:"subscribe"`
	MessageType  string     `mapstructure:"message_type"`
	BufferSize   int        `mapstructure:"buffer_size"`
	Heartbeat    *Heartbeat `mapstructure:"heartbeat"`
	Reconnect    *Reconnect `mapstructure:"reconnect"`
}

// Auth is the YAML shape for per-source authentication; the Kind
// field discriminates which of the other fields are honoured.
type Auth struct {
	Kind   string `mapstructure:"kind"` // bearer | api_key | basic | hmac | header
	Token  string `mapstructure:"token"`
	Header string `mapstructure:"header"`
	Secret string `mapstructure:"secret"`
	User   string `mapstructure:"user"`
	Pass   string `mapstructure:"pass"`
}

// Extract maps article fields to the response paths the source's
// driver pulls them from (JSON paths for HTTP/Webhook, CSS selectors
// for page scrape).
type Extract struct {
	Title     string `mapstructure:"title"`
	Content   string `mapstructure:"content"`
	Timestamp string `mapstructure:"timestamp"`
	Link      string `mapstructure:"link"`
	Items     string `mapstructure:"items"`
}

// Retry sets the exponential-backoff parameters httpx applies to a
// source's outbound requests.
type Retry struct {
	Max        int `mapstructure:"max"`
	BackoffMs  int `mapstructure:"backoff_ms"`
	MaxDelayMs int `mapstructure:"max_delay_ms"`
}

// Pagination describes how an HTTP source walks multi-page responses.
// CursorPath or NextUrlPath drives the next request; MaxPages caps
// the walk.
type Pagination struct {
	CursorPath  string `mapstructure:"cursor_path"`
	NextUrlPath string `mapstructure:"next_url_path"`
	PageParam   string `mapstructure:"page_param"`
	MaxPages    int    `mapstructure:"max_pages"`
}

// Heartbeat is the ping/pong interval for a websocket source.
type Heartbeat struct {
	IntervalMs int `mapstructure:"interval_ms"`
	TimeoutMs  int `mapstructure:"timeout_ms"`
}

// Reconnect is the reconnect policy for a websocket source. Disabled
// when Enabled=false; otherwise BackoffMs * 2^attempt capped at
// MaxDelayMs.
type Reconnect struct {
	Enabled    bool `mapstructure:"enabled"`
	BackoffMs  int  `mapstructure:"backoff_ms"`
	MaxDelayMs int  `mapstructure:"max_delay_ms"`
}
