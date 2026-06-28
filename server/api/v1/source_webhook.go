package v1

// WebhookSpec connector config for `type=webhook`. Inbound: at startup the
// driver registers Path on the public mux. Method/ContentType validate the
// request; Auth (on the parent Source) authenticates it.
type WebhookSpec struct {
	Path                string `json:"path"`
	Method              string `json:"method,omitempty"`
	ContentType         string `json:"content_type,omitempty"`
	MaxBodyBytes        int    `json:"max_body_bytes,omitempty"`
	ReplayWindowSeconds int    `json:"replay_window_seconds,omitempty"`
}

func (s *WebhookSpec) Kind() SourceType { return WebhookType }
