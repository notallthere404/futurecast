package httpx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestClient_Get_OK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`hello`))
	}))
	defer srv.Close()

	body, err := New(discardLogger()).Get(t.Context(), srv.URL, nil)
	if err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("body = %q, want hello", body)
	}
}

func TestClient_AppliesDefaultUserAgent(t *testing.T) {
	t.Parallel()
	var gotUA atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA.Store(r.Header.Get("User-Agent"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := New(discardLogger()).Get(t.Context(), srv.URL, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ua, _ := gotUA.Load().(string); ua == "" {
		t.Errorf("UA not applied")
	}
}

func TestClient_AppliesAuth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		auth *v1.Auth
		want func(*http.Request) bool
	}{
		{
			name: "bearer",
			auth: &v1.Auth{Kind: "bearer", Token: "abc"},
			want: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer abc" },
		},
		{
			name: "api_key default header",
			auth: &v1.Auth{Kind: "api_key", Token: "xyz"},
			want: func(r *http.Request) bool { return r.Header.Get("X-API-Key") == "xyz" },
		},
		{
			name: "api_key custom header",
			auth: &v1.Auth{Kind: "api_key", Header: "X-Custom", Token: "xyz"},
			want: func(r *http.Request) bool { return r.Header.Get("X-Custom") == "xyz" },
		},
		{
			name: "basic",
			auth: &v1.Auth{Kind: "basic", User: "u", Pass: "p"},
			want: func(r *http.Request) bool {
				u, p, ok := r.BasicAuth()
				return ok && u == "u" && p == "p"
			},
		},
		{
			name: "header",
			auth: &v1.Auth{Kind: "header", Header: "X-Token", Token: "t"},
			want: func(r *http.Request) bool { return r.Header.Get("X-Token") == "t" },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var passed atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				passed.Store(tc.want(r))
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			_, err := New(discardLogger()).Get(t.Context(), srv.URL, &Opts{Auth: tc.auth})
			if err != nil {
				t.Fatalf("Get err: %v", err)
			}
			if !passed.Load() {
				t.Errorf("auth header not applied as expected for %s", tc.name)
			}
		})
	}
}

func TestClient_AppliesCustomHeaders(t *testing.T) {
	t.Parallel()
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("X-Foo"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := New(discardLogger()).Get(t.Context(), srv.URL, &Opts{Headers: map[string]string{"X-Foo": "bar"}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v, _ := got.Load().(string); v != "bar" {
		t.Errorf("X-Foo = %q, want bar", v)
	}
}

func TestClient_4xx_NotRetried(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := New(discardLogger()).Get(t.Context(), srv.URL, &Opts{Retry: &v1.Retry{Max: 3, BackoffMs: 1}})
	if err == nil {
		t.Fatal("expected 4xx error")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (4xx is non-retryable)", calls.Load())
	}
}

func TestClient_5xx_Retries(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			http.Error(w, "fail", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	body, err := New(discardLogger()).Get(t.Context(), srv.URL, &Opts{Retry: &v1.Retry{Max: 3, BackoffMs: 1, MaxDelayMs: 5}})
	if err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want ok", body)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
}

func TestClient_5xx_ExhaustsRetries(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fail", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := New(discardLogger()).Get(t.Context(), srv.URL, &Opts{Retry: &v1.Retry{Max: 2, BackoffMs: 1, MaxDelayMs: 5}})
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if !strings.Contains(err.Error(), "max retries") {
		t.Errorf("err = %v, want 'max retries'", err)
	}
}

func TestClient_PostJSON_SendsBody(t *testing.T) {
	t.Parallel()
	var gotBody atomic.Value
	var gotType atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody.Store(string(b))
		gotType.Store(r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := New(discardLogger()).PostJSON(t.Context(), srv.URL, map[string]int{"a": 1}, nil)
	if err != nil {
		t.Fatalf("PostJSON err: %v", err)
	}
	if v, _ := gotBody.Load().(string); v != `{"a":1}` {
		t.Errorf("body = %q", v)
	}
	if v, _ := gotType.Load().(string); v != "application/json" {
		t.Errorf("content-type = %q", v)
	}
}

func TestDecodeJSON(t *testing.T) {
	t.Parallel()
	t.Run("empty body errors", func(t *testing.T) {
		t.Parallel()
		var v any
		if err := DecodeJSON(nil, &v); err == nil {
			t.Error("expected empty body error")
		}
	})
	t.Run("invalid json errors", func(t *testing.T) {
		t.Parallel()
		var v map[string]int
		if err := DecodeJSON([]byte("not-json"), &v); err == nil {
			t.Error("expected json error")
		}
	})
	t.Run("valid decodes", func(t *testing.T) {
		t.Parallel()
		var v map[string]int
		if err := DecodeJSON([]byte(`{"x":7}`), &v); err != nil {
			t.Fatalf("err: %v", err)
		}
		if v["x"] != 7 {
			t.Errorf("got %v", v)
		}
	})
}

func TestClient_CtxCancelDuringBackoff(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fail", http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err := New(discardLogger()).Get(ctx, srv.URL, &Opts{Retry: &v1.Retry{Max: 10, BackoffMs: 100, MaxDelayMs: 1000}})
	if err == nil {
		t.Fatal("expected ctx cancel error")
	}
}
