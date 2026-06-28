// Package inference owns the runtime classifier surface: a Client
// that dispatches classify requests to either the self-hosted Python
// service or an OpenAI-compatible remote API, plus Mode strategies
// that decide where articles come from (continuous DB drain vs
// manual per-request) and a Container helper that drives the local
// docker-compose lifecycle for self-hosted runtimes.
//
// The event loop that sequences refill / classify / persist lives in
// the inference controller (pkg/controller/inference), not here.
// This package is purely the building blocks the controller composes.
package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

// Target describes where the Client dispatches classify requests.
// Type discriminates between self-hosted modes (zeroshot, llm — sent
// to the Python service at Addr) and api mode (sent directly to an
// OpenAI-compatible endpoint at Endpoint, authenticated with APIKey).
type Target struct {
	Type     string // zeroshot | llm | api
	Addr     string // self-hosted Python service base URL
	Endpoint string // remote API base URL (api mode)
	APIKey   string // bearer token for the remote API (api mode)
	Model    string // model id; routed differently per Type
}

// ClassifyResponse is one item from a /classify response array
// (one per classification group in the request). The Go and Python
// paths produce the same shape so downstream persist logic doesn't
// care which backend ran.
type ClassifyResponse struct {
	Classification string                      `json:"classification"`
	ID             string                      `json:"id"`
	ArticleID      string                      `json:"article_id"`
	Timestamp      string                      `json:"timestamp"`
	Data           map[string][]*v1.LabelScore `json:"data"`
}

// Client is the wire-level interface to the classifier backend.
// It holds no queue or loop state; the controller drives it by
// calling Classify per article (or per batch).
type Client struct {
	log  *slog.Logger
	http *http.Client

	mu     sync.RWMutex
	target Target
}

// New returns a Client with a generous classify timeout. The first
// real request can take minutes on a cold model load; downstream
// callers can interrupt earlier via the per-call context.
func New(log *slog.Logger) *Client {
	return &Client{
		log:  log.With(slog.String("mod", "inference-client")),
		http: &http.Client{Timeout: (9 * time.Minute) + (50 * time.Second)},
	}
}

// SetTarget atomically replaces the dispatch target. Called by the
// inference controller on boot and on every config reload that
// changes type / model / endpoint / api_key.
func (c *Client) SetTarget(t Target) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.target = t
}

// Target returns a snapshot of the active target. Used by the
// controller to log diagnostics + decide whether the Python
// container needs to come up.
func (c *Client) Target() Target {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.target
}

// Info queries GET /info on the Python service. Only meaningful for
// self-hosted modes; api mode returns a zero-valued info plus a
// sentinel error so the controller knows not to compare.
func (c *Client) Info(ctx context.Context) (v1.InferenceInfo, error) {
	t := c.Target()
	if t.Type == "api" {
		return v1.InferenceInfo{}, ErrInfoNotSupported
	}

	var out v1.InferenceInfo
	path := t.Addr + "/info"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return out, fmt.Errorf("build info request: %w", err)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return out, fmt.Errorf("info: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return out, fmt.Errorf("read info body: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return out, fmt.Errorf("info bad status %d: %s", res.StatusCode, truncate(body, 256))
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("unmarshal info: %w", err)
	}
	return out, nil
}

// Load asks the Python service to swap its loaded model + type.
// Returns immediately after the service ACKs (202); callers poll
// /health for the state to flip back to "live" before resuming
// classify calls. No-op for api mode.
func (c *Client) Load(ctx context.Context, model, typ string) error {
	t := c.Target()
	if t.Type == "api" {
		return nil
	}

	payload, err := json.Marshal(v1.InferenceLoad{Type: typ, Model: model})
	if err != nil {
		return fmt.Errorf("marshal load: %w", err)
	}
	path := t.Addr + "/load"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("build load request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("read load body: %w", err)
	}
	if res.StatusCode != http.StatusAccepted && res.StatusCode != http.StatusOK {
		return fmt.Errorf("load bad status %d: %s", res.StatusCode, truncate(body, 256))
	}
	return nil
}

// Classify dispatches one article against the configured spec.
// Picks Python or the OpenAI-compatible chat path based on the
// active target's Type. Returns the per-classification response
// array, identical shape across backends.
func (c *Client) Classify(ctx context.Context, art *v1.ClassifyArticle, spec []v1.ClassificationSpec) ([]*ClassifyResponse, error) {
	if len(spec) == 0 {
		return nil, errors.New("no classification spec configured")
	}
	t := c.Target()
	switch t.Type {
	case "api":
		return c.classifyAPI(ctx, art, spec, t)
	default:
		return c.classifyPython(ctx, art, spec, t)
	}
}

// ErrInfoNotSupported is returned by Info when the active target
// doesn't expose a /info endpoint (api mode). Callers should check
// errors.Is(err, ErrInfoNotSupported) and skip the comparison.
var ErrInfoNotSupported = errors.New("info not supported for api targets")

// classifyPython sends the whole spec to the self-hosted Python
// service. The service iterates per-attribute internally and
// returns one ClassifyResponse per classification group.
func (c *Client) classifyPython(ctx context.Context, art *v1.ClassifyArticle, spec []v1.ClassificationSpec, target Target) ([]*ClassifyResponse, error) {
	req := v1.ClassifyRequest{
		ID:              art.ID,
		Content:         art.Content,
		Timestamp:       art.Timestamp.Format(time.RFC3339),
		Classifications: spec,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	url := target.Addr + "/classify"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status %d: %s", res.StatusCode, truncate(body, 256))
	}

	var output []*ClassifyResponse
	if err := json.Unmarshal(body, &output); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	return output, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
