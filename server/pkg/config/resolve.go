package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

//
// Resolve flattens the YAML SourceConfig into the []*v1.Source rows
// the system controller writes to the database. Each per-kind list is
// walked; child entries inherit defaults from the parent block.
//
// Inheritance rule: empty / zero on the child = use parent. Lists and
// maps replace, never merge; set the child field to nil to inherit, to
// an empty slice / map to disable inherited filters explicitly.

func (s *SourceConfig) Resolve() ([]*v1.Source, error) {
	out := make([]*v1.Source, 0, len(s.RSS)+len(s.HTTP)+len(s.Webhook)+len(s.Websocket))

	for i := range s.RSS {
		src, err := s.resolveRss(&s.RSS[i])
		if err != nil {
			return nil, fmt.Errorf("source.rss[%d]: %w", i, err)
		}
		out = append(out, src)
	}
	for i := range s.HTTP {
		src, err := s.resolveHttp(&s.HTTP[i])
		if err != nil {
			return nil, fmt.Errorf("source.http[%d]: %w", i, err)
		}
		out = append(out, src)
	}
	for i := range s.Webhook {
		src, err := s.resolveWebhook(&s.Webhook[i])
		if err != nil {
			return nil, fmt.Errorf("source.webhook[%d]: %w", i, err)
		}
		out = append(out, src)
	}
	for i := range s.Websocket {
		src, err := s.resolveWebsocket(&s.Websocket[i])
		if err != nil {
			return nil, fmt.Errorf("source.websocket[%d]: %w", i, err)
		}
		out = append(out, src)
	}

	return out, nil
}

// resolveCommon fill v1.Source fields shared across every kind and
// apply parent-default inheritance.
func (s *SourceConfig) resolveCommon(c *CommonFields, kind v1.SourceType) *v1.Source {
	tags := c.Tags
	if tags == nil {
		// sources.tags is NOT NULL with DEFAULT '{}'. The default only
		// applies when the column is omitted; CopyFrom always writes
		// every column, so a nil slice would land as NULL. Normalise
		// here once.
		tags = []string{}
	}

	src := &v1.Source{
		Type:        kind,
		Name:        c.Name,
		URL:         c.URL,
		Active:      c.Active,
		Trust:       v1.Trust(firstNonEmpty(c.Trust, s.Default.Trust)),
		Description: c.Description,
		Tags:        tags,
	}

	if to := c.TimeoutSeconds; to > 0 {
		t := to
		src.TimeoutSeconds = &t
	} else if s.Default.TimeoutSeconds > 0 {
		t := s.Default.TimeoutSeconds
		src.TimeoutSeconds = &t
	}

	src.Auth = mapAuth(c.Auth)
	src.Extract = mapExtract(c.Extract)
	src.Retry = mapRetry(firstRetry(c.Retry, s.Default.Retry))
	src.Headers = mergeHeaders(c.Headers, s.Default.Headers)

	return src
}

func (s *SourceConfig) resolveRss(r *RSSConfig) (*v1.Source, error) {
	src := s.resolveCommon(&r.CommonFields, v1.RSSType)

	spec := &v1.RSSSpec{
		URL:      r.URL,
		Target:   v1.SourceTarget(r.Target),
		Limit:    r.Limit,
		Schedule: firstNonEmpty(r.Schedule, s.Default.Schedule),
		Paths:    r.Paths,
	}
	if len(r.Selectors) > 0 {
		spec.Selectors = make(v1.SelectorMap, len(r.Selectors))
		for k, v := range r.Selectors {
			spec.Selectors[v1.SelectorType(k)] = v
		}
	}
	return finalizeSource(src, spec)
}

func (s *SourceConfig) resolveHttp(h *HTTPConfig) (*v1.Source, error) {
	src := s.resolveCommon(&h.CommonFields, v1.HTTPType)

	spec := &v1.HTTPSpec{
		Method:   h.Method,
		Schedule: firstNonEmpty(h.Schedule, s.Default.Schedule),
		Query:    h.Query,
		Body:     h.Body,
	}
	if h.Pagination != nil {
		spec.Pagination = &v1.Pagination{
			CursorPath:  h.Pagination.CursorPath,
			NextUrlPath: h.Pagination.NextUrlPath,
			PageParam:   h.Pagination.PageParam,
			MaxPages:    h.Pagination.MaxPages,
		}
	}
	return finalizeSource(src, spec)
}

func (s *SourceConfig) resolveWebhook(w *WebhookConfig) (*v1.Source, error) {
	src := s.resolveCommon(&w.CommonFields, v1.WebhookType)
	// Webhook routes need a stable URL for the unique constraint on
	// sources.url; synthesise from the path.
	if src.URL == "" {
		src.URL = "webhook://" + w.Path
	}

	spec := &v1.WebhookSpec{
		Path:                w.Path,
		Method:              w.Method,
		ContentType:         w.ContentType,
		MaxBodyBytes:        w.MaxBodyBytes,
		ReplayWindowSeconds: w.ReplayWindowSeconds,
	}
	return finalizeSource(src, spec)
}

func (s *SourceConfig) resolveWebsocket(ws *WebsocketConfig) (*v1.Source, error) {
	src := s.resolveCommon(&ws.CommonFields, v1.WebsocketType)

	spec := &v1.WebsocketSpec{
		Protocols:   ws.Protocols,
		Subscribe:   ws.Subscribe,
		MessageType: ws.MessageType,
		BufferSize:  ws.BufferSize,
	}
	if ws.Heartbeat != nil {
		spec.Heartbeat = &v1.Heartbeat{
			IntervalMs: ws.Heartbeat.IntervalMs,
			TimeoutMs:  ws.Heartbeat.TimeoutMs,
		}
	}
	if ws.Reconnect != nil {
		spec.Reconnect = &v1.Reconnect{
			Enabled:    ws.Reconnect.Enabled,
			BackoffMs:  ws.Reconnect.BackoffMs,
			MaxDelayMs: ws.Reconnect.MaxDelayMs,
		}
	}
	return finalizeSource(src, spec)
}

// finalizeSource marshal the Spec into SpecRaw and compute the
// content hash used by syncSources to detect changes.
func finalizeSource(src *v1.Source, spec v1.Spec) (*v1.Source, error) {
	src.Spec = spec
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	src.SpecRaw = raw

	h, err := hashSource(src)
	if err != nil {
		return nil, err
	}
	src.Hash = h
	return src, nil
}

func hashSource(src *v1.Source) ([]byte, error) {
	// Hash the parts that should trigger a re-upsert when changed.
	// Created/Updated timestamps are excluded so DB round-trips don't
	// shift the hash.
	payload := struct {
		Type           v1.SourceType
		Name, URL      string
		Active         bool
		Trust          v1.Trust
		Description    string
		Tags           []string
		TimeoutSeconds *int
		Auth           *v1.Auth
		Extract        *v1.Extract
		Retry          *v1.Retry
		Headers        v1.Headers
		SpecRaw        json.RawMessage
	}{
		src.Type, src.Name, src.URL, src.Active, src.Trust,
		src.Description, src.Tags, src.TimeoutSeconds,
		src.Auth, src.Extract, src.Retry, src.Headers, src.SpecRaw,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("hash payload: %w", err)
	}
	sum := sha256.Sum256(b)
	return sum[:], nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func firstRetry(a, b *Retry) *Retry {
	if a != nil {
		return a
	}
	return b
}

func mapAuth(a *Auth) *v1.Auth {
	if a == nil {
		return nil
	}
	return &v1.Auth{
		Kind: a.Kind, Token: a.Token, Header: a.Header,
		Secret: a.Secret, User: a.User, Pass: a.Pass,
	}
}

func mapExtract(e *Extract) *v1.Extract {
	if e == nil {
		return nil
	}
	return &v1.Extract{
		Title: e.Title, Content: e.Content, Timestamp: e.Timestamp,
		Link: e.Link, Items: e.Items,
	}
}

func mapRetry(r *Retry) *v1.Retry {
	if r == nil {
		return nil
	}
	return &v1.Retry{Max: r.Max, BackoffMs: r.BackoffMs, MaxDelayMs: r.MaxDelayMs}
}

func mergeHeaders(child, parent map[string]string) v1.Headers {
	if len(child) > 0 {
		return v1.Headers(child)
	}
	if len(parent) > 0 {
		return v1.Headers(parent)
	}
	return nil
}

// ClassificationMap returns the classification-to-attribute-names mapping
// the system controller uses to sync DB tables.
func (i *Inference) ClassificationMap() map[string][]string {
	out := make(map[string][]string, len(i.Classifications))
	for cls, attrs := range i.Classifications {
		names := make([]string, 0, len(attrs))
		for _, a := range attrs {
			names = append(names, a.Name)
		}
		out[cls] = names
	}
	return out
}

// InferenceSpec flattens the live config into the wire shape the
// Python inference service expects per request. Effective cutoff /
// top_n are resolved against the inference-block defaults.
func (c *Config) InferenceSpec() []v1.ClassificationSpec {
	out := make([]v1.ClassificationSpec, 0, len(c.Inference.Classifications))
	for name, attrs := range c.Inference.Classifications {
		specs := make([]v1.AttributeSpec, 0, len(attrs))
		for _, a := range attrs {
			specs = append(specs, v1.AttributeSpec{
				Name:        a.Name,
				Labels:      labelSpecs(a.Labels),
				Prompt:      a.Prompt,
				Instruction: a.Instruction,
				TopN:        a.EffectiveTopN(c.Inference.Default.TopN),
				Cutoff:      a.EffectiveCutoff(c.Inference.Default.Cutoff),
			})
		}
		out = append(out, v1.ClassificationSpec{
			Name:       name,
			Attributes: specs,
		})
	}
	return out
}

func labelSpecs(ls []InferenceLabel) []v1.LabelSpec {
	out := make([]v1.LabelSpec, 0, len(ls))
	for _, l := range ls {
		out = append(out, v1.LabelSpec{Name: l.Name, Definition: l.Definition})
	}
	return out
}

// ClientConfig is the payload the dashboard renders for the config editor and
// label-filter dropdowns. Raw is the YAML text shown in the editor;
// ClassMap drives the per-classification attribute UI.
type ClientConfig struct {
	Raw      string                        `json:"raw"`
	ClassMap map[string][]*ClientAttribute `json:"class"`
}

// ClientAttribute is the slim per-attribute shape the dashboard's
// label-filter dropdowns consume — name plus the flat label list.
type ClientAttribute struct {
	Name   string   `json:"name"`
	Labels []string `json:"labels"`
}

// LabelMap walks every label across every classification and assigns a
// deterministic numeric index. Used by 3D scatter and similar plots
// that need a stable label-to-axis-position mapping.
//
// Iteration over the classifications map is not order-stable but the
// returned indices ARE stable per process run, because the resulting
// map is keyed by label name. Two processes loaded from the same
// config will not necessarily produce identical numbers; that's fine
// for visualisation but document if you ever depend on it across runs.
func (c *Config) LabelMap() map[string]float64 {
	out := make(map[string]float64)
	var idx float64
	for _, attrs := range c.Inference.Classifications {
		for _, a := range attrs {
			for _, l := range a.Labels {
				if _, seen := out[l.Name]; seen {
					continue
				}
				out[l.Name] = idx
				idx++
			}
		}
	}
	return out
}

// ToClientConfig builds the dashboard payload from the live Config plus
// the raw YAML source (loaded separately by the config controller).
func (c *Config) ToClientConfig(raw string) ClientConfig {
	out := ClientConfig{
		Raw:      raw,
		ClassMap: make(map[string][]*ClientAttribute, len(c.Inference.Classifications)),
	}
	for cls, attrs := range c.Inference.Classifications {
		list := make([]*ClientAttribute, 0, len(attrs))
		for _, a := range attrs {
			labels := make([]string, 0, len(a.Labels))
			for _, l := range a.Labels {
				labels = append(labels, l.Name)
			}
			list = append(list, &ClientAttribute{Name: a.Name, Labels: labels})
		}
		out.ClassMap[cls] = list
	}
	return out
}
