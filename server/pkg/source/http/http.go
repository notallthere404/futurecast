// Package http is the driver for SourceType=http. Scheduled JSON
// fetcher: uses httpx for transport (UA/timeout/retry/auth) and
// HTTPSpec for method/body/query. Decoded payloads map into v1.Article
// via the source's Extract paths — Items selects the array of items
// inside the response, Title/Content/Timestamp/Link are dot-paths into
// each item.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/notallthere404/futurecast/server/pkg/httpx"
	"github.com/notallthere404/futurecast/server/pkg/utils"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

// HTTP implements source.Retriever for plain JSON endpoints, fetching
// once per call via the shared httpx.Client.
type HTTP struct {
	log    *slog.Logger
	client *httpx.Client
}

// New returns an HTTP retriever bound to the shared httpx.Client so
// retries and headers stay consistent across all outbound calls.
func New(log *slog.Logger, client *httpx.Client) *HTTP {
	return &HTTP{
		log:    log.With(slog.String("source", "http")),
		client: client,
	}
}

// Kind reports which SourceType this driver handles.
func (h *HTTP) Kind() v1.SourceType { return v1.HTTPType }

// Fetch performs one http request against src per its HTTPSpec.
// Sync; caller's goroutine (cron tick) blocks until done or ctx expires.
func (h *HTTP) Fetch(ctx context.Context, src *v1.Source) ([]*v1.Article, error) {
	spec := src.HTTP()
	if spec == nil {
		return nil, fmt.Errorf("http: source %s has wrong spec type %T", src.ID, src.Spec)
	}

	req, err := buildRequest(ctx, src, spec)
	if err != nil {
		return nil, err
	}

	body, err := h.client.Do(ctx, req, &httpx.Opts{
		Headers: map[string]string(src.Headers),
		Auth:    src.Auth,
		Retry:   src.Retry,
	})
	if err != nil {
		return nil, err
	}

	articles, err := extractArticles(src, body)
	if err != nil {
		return nil, fmt.Errorf("http: extract from %s: %w", src.Name, err)
	}
	h.log.Debug("http source fetched", "source", src.Name, "bytes", len(body), "articles", len(articles))
	return articles, nil
}

// extractArticles decodes the response body and walks Extract paths
// to build articles. Items picks the items array out of the response;
// "" or "$" means the body itself is the array. Per-item Title /
// Content / Timestamp / Link use the same dot-path semantics as the
// webhook driver (see api/v1/extract.go).
func extractArticles(src *v1.Source, body []byte) ([]*v1.Article, error) {
	if src.Extract == nil {
		return nil, errors.New("extract not configured")
	}

	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode body: %w", err)
	}

	items, err := selectItems(src.Extract.Items, decoded)
	if err != nil {
		return nil, err
	}

	out := make([]*v1.Article, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, intoArticle(src, item))
	}
	return out, nil
}

// selectItems returns the array of items the rest of the extract
// targets. Empty / "$" returns the whole body if it's already an
// array; otherwise walks the dot-path and expects an array there.
func selectItems(path string, body any) ([]any, error) {
	if path == "" || path == "$" {
		arr, ok := body.([]any)
		if !ok {
			return nil, fmt.Errorf("items path %q: body is not an array", path)
		}
		return arr, nil
	}
	v, ok := keyWalk(path, body)
	if !ok {
		return nil, fmt.Errorf("items path %q: not found in body", path)
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("items path %q: not an array", path)
	}
	return arr, nil
}

// keyWalk descends dot-separated keys through nested maps. Duplicates
// api/v1.keyWalk because that one is unexported; the inline copy keeps
// this driver from depending on v1's internals.
func keyWalk(path string, data any) (any, bool) {
	tmp := data
	for _, k := range strings.Split(path, ".") {
		m, ok := tmp.(map[string]any)
		if !ok {
			return nil, false
		}
		tmp, ok = m[k]
		if !ok {
			return nil, false
		}
	}
	return tmp, true
}

// intoArticle builds a v1.Article from one decoded JSON item using
// the source's Extract paths. Missing fields fall back to sensible
// defaults: timestamp = now, ID derived from link (UUIDv5) or random
// (UUIDv4) when the link is also missing.
func intoArticle(src *v1.Source, item map[string]any) *v1.Article {
	title, _ := v1.ExtractString(src.Extract.Title, item)
	content, _ := v1.ExtractString(src.Extract.Content, item)
	link, _ := v1.ExtractString(src.Extract.Link, item)

	ts, ok := v1.ExtractTime(src.Extract.Timestamp, item)
	if !ok {
		ts = time.Now()
	}

	return &v1.Article{
		ID:         utils.NewArticleID(link),
		SourceID:   src.ID,
		SourceType: v1.HTTPType,
		Title:      title,
		Content:    content,
		Timestamp:  ts,
		Link:       link,
		Processed:  false,
	}
}

func buildRequest(ctx context.Context, src *v1.Source, spec *v1.HTTPSpec) (*stdhttp.Request, error) {
	method := spec.Method
	if method == "" {
		method = stdhttp.MethodGet
	}

	u, err := url.Parse(src.URL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if len(spec.Query) > 0 {
		q := u.Query()
		for k, v := range spec.Query {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}

	var body io.Reader
	if spec.Body != "" {
		body = strings.NewReader(spec.Body)
	}

	req, err := stdhttp.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	return req, nil
}
