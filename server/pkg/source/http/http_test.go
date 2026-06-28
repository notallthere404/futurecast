package http

import (
	"context"
	"io"
	stdhttp "net/http"
	"testing"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func TestBuildRequest_DefaultsMethodToGET(t *testing.T) {
	t.Parallel()
	src := &v1.Source{URL: "https://example.com/api"}
	req, err := buildRequest(t.Context(), src, &v1.HTTPSpec{})
	if err != nil {
		t.Fatalf("buildRequest err: %v", err)
	}
	if req.Method != stdhttp.MethodGet {
		t.Errorf("method = %q, want GET", req.Method)
	}
	if req.URL.String() != "https://example.com/api" {
		t.Errorf("url = %q", req.URL.String())
	}
}

func TestBuildRequest_AppendsQueryParams(t *testing.T) {
	t.Parallel()
	src := &v1.Source{URL: "https://example.com/api?keep=1"}
	spec := &v1.HTTPSpec{Query: map[string]string{"page": "2", "limit": "50"}}
	req, err := buildRequest(t.Context(), src, spec)
	if err != nil {
		t.Fatalf("buildRequest err: %v", err)
	}
	q := req.URL.Query()
	if q.Get("keep") != "1" || q.Get("page") != "2" || q.Get("limit") != "50" {
		t.Errorf("query lost params: %v", q)
	}
}

func TestBuildRequest_BodyAttached(t *testing.T) {
	t.Parallel()
	src := &v1.Source{URL: "https://example.com/api"}
	spec := &v1.HTTPSpec{Method: stdhttp.MethodPost, Body: `{"x":1}`}
	req, err := buildRequest(t.Context(), src, spec)
	if err != nil {
		t.Fatalf("buildRequest err: %v", err)
	}
	if req.Method != stdhttp.MethodPost {
		t.Errorf("method = %q, want POST", req.Method)
	}
	b, _ := io.ReadAll(req.Body)
	if string(b) != `{"x":1}` {
		t.Errorf("body = %q", b)
	}
}

func TestBuildRequest_BadURL(t *testing.T) {
	t.Parallel()
	src := &v1.Source{URL: "://broken"}
	if _, err := buildRequest(t.Context(), src, &v1.HTTPSpec{}); err == nil {
		t.Error("expected url parse error")
	}
}

func TestExtractArticles_RootArray(t *testing.T) {
	t.Parallel()
	body := []byte(`[
		{"t":"A","c":"alpha","ts":"2026-06-24T08:00:00Z","l":"https://e.x/a"},
		{"t":"B","c":"beta","ts":"2026-06-24T09:00:00Z","l":"https://e.x/b"}
	]`)
	src := &v1.Source{
		ID:   "src-1",
		URL:  "https://e.x",
		Type: v1.HTTPType,
		Extract: &v1.Extract{
			Items: "$", Title: "t", Content: "c", Timestamp: "ts", Link: "l",
		},
	}
	arts, err := extractArticles(src, body)
	if err != nil {
		t.Fatalf("extractArticles: %v", err)
	}
	if len(arts) != 2 {
		t.Fatalf("got %d articles, want 2", len(arts))
	}
	if arts[0].Title != "A" || arts[0].Content != "alpha" || arts[0].Link != "https://e.x/a" {
		t.Errorf("article[0] = %+v", arts[0])
	}
	if arts[0].SourceType != v1.HTTPType {
		t.Errorf("source type = %q, want http", arts[0].SourceType)
	}
	if arts[0].Timestamp.IsZero() {
		t.Error("timestamp not parsed")
	}
}

func TestExtractArticles_NestedItems(t *testing.T) {
	t.Parallel()
	body := []byte(`{"data": {"items": [{"t":"X","c":"x-content","l":"https://e.x/x"}]}}`)
	src := &v1.Source{
		Extract: &v1.Extract{
			Items: "data.items", Title: "t", Content: "c", Link: "l",
		},
	}
	arts, err := extractArticles(src, body)
	if err != nil {
		t.Fatalf("extractArticles: %v", err)
	}
	if len(arts) != 1 || arts[0].Title != "X" {
		t.Errorf("articles = %+v", arts)
	}
}

func TestExtractArticles_MissingExtract(t *testing.T) {
	t.Parallel()
	if _, err := extractArticles(&v1.Source{}, []byte(`[]`)); err == nil {
		t.Error("expected error for nil Extract")
	}
}

func TestExtractArticles_BadJSON(t *testing.T) {
	t.Parallel()
	src := &v1.Source{Extract: &v1.Extract{Items: "$"}}
	if _, err := extractArticles(src, []byte(`not json`)); err == nil {
		t.Error("expected decode error")
	}
}

func TestExtractArticles_ItemsNotArray(t *testing.T) {
	t.Parallel()
	src := &v1.Source{Extract: &v1.Extract{Items: "$"}}
	if _, err := extractArticles(src, []byte(`{"x":1}`)); err == nil {
		t.Error("expected 'not an array' error for object body with items=$")
	}
}

func TestExtractArticles_MissingTimestamp_DefaultsToNow(t *testing.T) {
	t.Parallel()
	body := []byte(`[{"t":"A","c":"x","l":"https://e.x/a"}]`)
	src := &v1.Source{
		Extract: &v1.Extract{Items: "$", Title: "t", Content: "c", Link: "l", Timestamp: "ts"},
	}
	arts, err := extractArticles(src, body)
	if err != nil {
		t.Fatalf("extractArticles: %v", err)
	}
	if arts[0].Timestamp.IsZero() {
		t.Error("missing timestamp should default to time.Now(), got zero")
	}
}

func TestBuildRequest_CarriesContext(t *testing.T) {
	t.Parallel()
	type key struct{}
	ctx := context.WithValue(t.Context(), key{}, "value")

	src := &v1.Source{URL: "https://example.com/api"}
	req, err := buildRequest(ctx, src, &v1.HTTPSpec{})
	if err != nil {
		t.Fatalf("buildRequest err: %v", err)
	}
	if req.Context().Value(key{}) != "value" {
		t.Errorf("ctx value not propagated")
	}
}
