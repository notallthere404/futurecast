package v1

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// Spec is the polymorphic, type-specific portion of a Source.
// Each SourceType has exactly one Spec implementation. Stored as JSONB in
// the `sources.spec` column; dispatched by SourceType on read.
type Spec interface {
	Kind() SourceType
}

// Auth credential config shared by HTTP-like connectors.
type Auth struct {
	Kind   string `json:"kind"` // "bearer" | "api_key" | "basic" | "hmac" | "header"
	Token  string `json:"token,omitempty"`
	Header string `json:"header,omitempty"`
	Secret string `json:"secret,omitempty"`
	User   string `json:"user,omitempty"`
	Pass   string `json:"pass,omitempty"`
}

// Extract is the payload-to-article mapping. JSON paths for HTTP/Webhook/WS,
// optional override for RSS field mapping.
type Extract struct {
	Title     string `json:"title,omitempty"`
	Content   string `json:"content,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	Link      string `json:"link,omitempty"`
	Items     string `json:"items,omitempty"` // array path for HTTP responses
}

// Retry transient failure policy. Used by polling and reconnecting drivers.
type Retry struct {
	Max        int `json:"max"`
	BackoffMs  int `json:"backoff_ms"`
	MaxDelayMs int `json:"max_delay_ms"`
}

// Headers is a named map so it can carry Scan/Value methods. Behaves like
// map[string]string at call sites (indexing, range, len all work).
type Headers map[string]string

//
// pgx invokes Scan when reading jsonb into a Go field with Scanner; it
// invokes Value when serializing a Go field carrying Valuer. Together
// these let Auth/Extract/Retry/Headers round-trip jsonb without custom
// codecs or pre-marshal dances at call sites.

func (a *Auth) Scan(src any) error          { return scanJSONB(src, a) }
func (a Auth) Value() (driver.Value, error) { return json.Marshal(a) }

func (e *Extract) Scan(src any) error          { return scanJSONB(src, e) }
func (e Extract) Value() (driver.Value, error) { return json.Marshal(e) }

func (r *Retry) Scan(src any) error          { return scanJSONB(src, r) }
func (r Retry) Value() (driver.Value, error) { return json.Marshal(r) }

func (h *Headers) Scan(src any) error { return scanJSONB(src, h) }
func (h Headers) Value() (driver.Value, error) {
	if h == nil {
		return nil, nil
	}
	return json.Marshal(h)
}

// scanJSONB common Scan body. Accepts []byte or string from pgx (jsonb
// arrives as either depending on codec path), nil for SQL NULL.
func scanJSONB(src, dst any) error {
	if src == nil {
		return nil
	}
	var data []byte
	switch v := src.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("jsonb scan: unsupported source type %T", src)
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, dst)
}

// UnmarshalSpec decodes raw JSONB into the concrete Spec for the given type.
// Used by Source.UnmarshalJSON and by registry code when scanning rows.
func UnmarshalSpec(kind SourceType, raw json.RawMessage) (Spec, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	switch kind {
	case RSSType:
		var s RSSSpec
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("rss spec: %w", err)
		}
		return &s, nil
	case WebhookType:
		var s WebhookSpec
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("webhook spec: %w", err)
		}
		return &s, nil
	case HTTPType:
		var s HTTPSpec
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("http spec: %w", err)
		}
		return &s, nil
	case WebsocketType:
		var s WebsocketSpec
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("websocket spec: %w", err)
		}
		return &s, nil
	default:
		return nil, fmt.Errorf("unknown source type: %s", kind)
	}
}
