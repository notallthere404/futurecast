package v1

// WebsocketSpec connector config for `type=websocket`. Long-running driver.
type WebsocketSpec struct {
	Protocols   []string   `json:"protocols,omitempty"`
	Subscribe   string     `json:"subscribe,omitempty"`
	Heartbeat   *Heartbeat `json:"heartbeat,omitempty"`
	Reconnect   *Reconnect `json:"reconnect,omitempty"`
	MessageType string     `json:"message_type,omitempty"` // "json" | "text" | "binary"
	BufferSize  int        `json:"buffer_size,omitempty"`
}

func (s *WebsocketSpec) Kind() SourceType { return WebsocketType }

// Heartbeat ping/pong policy.
type Heartbeat struct {
	IntervalMs int `json:"interval_ms"`
	TimeoutMs  int `json:"timeout_ms"`
}

// Reconnect reconnection policy for long-running connections.
type Reconnect struct {
	Enabled    bool `json:"enabled"`
	BackoffMs  int  `json:"backoff_ms,omitempty"`
	MaxDelayMs int  `json:"max_delay_ms,omitempty"`
}
