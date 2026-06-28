// Package httpx; shared outbound HTTP client. Centralises timeout,
// user-agent, headers, auth, and retry policy so source drivers and
// inference call the same transport with the same observability.
//
// httpx is transport-only. Concurrency (fan-out, semaphores) belongs to
// callers because policy differs per use-case.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

const (
	defaultTimeout = 30 * time.Second
	defaultUA      = "ng-tm/1.0 (+https://github.com/notallthere/ng-tm)"
)

// Client wraps net/http.Client with default headers and retry policy.
// Safe for concurrent use; the underlying http.Client is.
type Client struct {
	log *slog.Logger
	c   *http.Client
	ua  string
}

// New returns a Client with the default 30-second timeout. Use
// NewWithTimeout when a different value is needed (long-poll, batch).
func New(log *slog.Logger) *Client {
	return NewWithTimeout(log, defaultTimeout)
}

// NewWithTimeout returns a Client with a caller-chosen timeout applied
// to every request via the underlying http.Client.
func NewWithTimeout(log *slog.Logger, timeout time.Duration) *Client {
	return &Client{
		log: log.With(slog.String("mod", "httpx")),
		c:   &http.Client{Timeout: timeout},
		ua:  defaultUA,
	}
}

// Opts carries per-request overrides extracted from the calling
// domain (e.g. v1.Source.Auth/Headers/Retry). Zero-valued fields are
// skipped.
type Opts struct {
	Headers map[string]string
	Auth    *v1.Auth
	Retry   *v1.Retry
}

// Do executes req with opts applied. Retries on transient failures up to
// Retry.Max with exponential backoff (BackoffMs … MaxDelayMs).
//
// Returns the response body bytes. Caller decides how to decode.
func (cl *Client) Do(ctx context.Context, req *http.Request, opts *Opts) ([]byte, error) {
	if opts == nil {
		opts = &Opts{}
	}
	applyHeaders(req, cl.ua, opts)

	max, backoff, maxDelay := retryParams(opts.Retry)
	var lastErr error

	for attempt := 0; attempt <= max; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(backoff, maxDelay, attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		body, retryable, err := cl.attempt(ctx, req)
		if err == nil {
			return body, nil
		}

		lastErr = err
		cl.log.Debug("request failed", "attempt", attempt+1, "url", req.URL.String(), "error", err)

		if !retryable {
			return nil, err
		}
	}

	return nil, fmt.Errorf("httpx: max retries: %w", lastErr)
}

func (cl *Client) attempt(ctx context.Context, req *http.Request) ([]byte, bool, error) {
	r := req.Clone(ctx)
	res, err := cl.c.Do(r)
	if err != nil {
		return nil, true, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, true, fmt.Errorf("read body: %w", err)
	}

	if res.StatusCode >= 500 {
		return nil, true, fmt.Errorf("status %d: %s", res.StatusCode, truncate(body, 256))
	}
	if res.StatusCode >= 400 {
		return nil, false, fmt.Errorf("status %d: %s", res.StatusCode, truncate(body, 256))
	}

	return body, false, nil
}

// Get convenience for GET requests.
func (cl *Client) Get(ctx context.Context, url string, opts *Opts) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	return cl.Do(ctx, req, opts)
}

// PostJSON convenience for POSTing a JSON-encoded body.
func (cl *Client) PostJSON(ctx context.Context, url string, body any, opts *Opts) ([]byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return cl.Do(ctx, req, opts)
}

// DecodeJSON runs Do then json.Unmarshal in one call. `out` must be a pointer.
func DecodeJSON(body []byte, out any) error {
	if len(body) == 0 {
		return errors.New("empty body")
	}
	return json.Unmarshal(body, out)
}

func applyHeaders(req *http.Request, ua string, opts *Opts) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", ua)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
	if opts.Auth != nil {
		applyAuth(req, opts.Auth)
	}
}

func applyAuth(req *http.Request, a *v1.Auth) {
	switch a.Kind {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+a.Token)
	case "api_key":
		h := a.Header
		if h == "" {
			h = "X-API-Key"
		}
		req.Header.Set(h, a.Token)
	case "basic":
		req.SetBasicAuth(a.User, a.Pass)
	case "header":
		req.Header.Set(a.Header, a.Token)
	}
}

func retryParams(r *v1.Retry) (max, backoffMs, maxDelayMs int) {
	if r == nil {
		return 0, 0, 0
	}
	return r.Max, r.BackoffMs, r.MaxDelayMs
}

func backoffDelay(backoffMs, maxDelayMs, attempt int) time.Duration {
	if backoffMs <= 0 {
		backoffMs = 250
	}
	d := backoffMs << (attempt - 1) // exponential
	if maxDelayMs > 0 && d > maxDelayMs {
		d = maxDelayMs
	}
	return time.Duration(d) * time.Millisecond
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
