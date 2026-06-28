package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

// signBody returns the hex SHA256 HMAC of body using secret.
func signBody(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func newSource(id, path string) *v1.Source {
	return &v1.Source{
		ID:   id,
		Type: v1.WebhookType,
		Spec: &v1.WebhookSpec{Path: path},
		Extract: &v1.Extract{
			Title:     "title",
			Content:   "body",
			Timestamp: "ts",
			Link:      "link",
		},
	}
}

func TestWebhook_ServeHTTP_ValidPayload(t *testing.T) {
	t.Parallel()

	out := make(chan *v1.Article, 1)
	wh := New(discardLogger())
	_ = wh.Start(t.Context(), out)
	wh.Register(newSource("s1", "feed"))

	body := `{"title":"hello","body":"content here","link":"https://x/y","ts":"2025-01-02T03:04:05Z"}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/feed", strings.NewReader(body))
	rec := httptest.NewRecorder()
	wh.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}

	select {
	case got := <-out:
		if got.Title != "hello" || got.Content != "content here" || got.Link != "https://x/y" {
			t.Errorf("article fields: %+v", got)
		}
		wantTS, _ := time.Parse(time.RFC3339, "2025-01-02T03:04:05Z")
		if !got.Timestamp.Equal(wantTS) {
			t.Errorf("Timestamp = %v, want %v", got.Timestamp, wantTS)
		}
		if got.SourceID != "s1" || got.SourceType != v1.WebhookType {
			t.Errorf("source fields: id=%q type=%q", got.SourceID, got.SourceType)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for article")
	}
}

func TestWebhook_ServeHTTP_UnknownPath(t *testing.T) {
	t.Parallel()
	wh := New(discardLogger())
	_ = wh.Start(t.Context(), make(chan *v1.Article, 1))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/missing", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	wh.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWebhook_ServeHTTP_InvalidJSON(t *testing.T) {
	t.Parallel()
	wh := New(discardLogger())
	_ = wh.Start(t.Context(), make(chan *v1.Article, 1))
	wh.Register(newSource("s1", "feed"))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/feed", strings.NewReader("not-json"))
	rec := httptest.NewRecorder()
	wh.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestWebhook_ServeHTTP_MissingContent(t *testing.T) {
	t.Parallel()
	wh := New(discardLogger())
	_ = wh.Start(t.Context(), make(chan *v1.Article, 1))
	wh.Register(newSource("s1", "feed"))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/feed", strings.NewReader(`{"title":"x"}`))
	rec := httptest.NewRecorder()
	wh.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// ---------- HMAC auth ----------

func newSignedSource(id, path, secret string) *v1.Source {
	s := newSource(id, path)
	s.Auth = &v1.Auth{Kind: "hmac", Header: "X-Signature", Secret: secret}
	return s
}

func TestWebhook_ServeHTTP_HMAC_ValidSig_Accepts(t *testing.T) {
	t.Parallel()
	out := make(chan *v1.Article, 1)
	wh := New(discardLogger())
	_ = wh.Start(t.Context(), out)
	wh.Register(newSignedSource("s1", "feed", "shh"))

	body := `{"title":"t","body":"long enough content here","ts":"2026-06-24T08:00:00Z","link":"https://e.x/a"}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/feed", strings.NewReader(body))
	req.Header.Set("X-Signature", signBody("shh", body))

	rec := httptest.NewRecorder()
	wh.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d body=%q, want 202", rec.Code, rec.Body.String())
	}
}

func TestWebhook_ServeHTTP_HMAC_MissingSig_Rejects(t *testing.T) {
	t.Parallel()
	wh := New(discardLogger())
	_ = wh.Start(t.Context(), make(chan *v1.Article, 1))
	wh.Register(newSignedSource("s1", "feed", "shh"))

	body := `{"title":"t","body":"x","ts":"2026-06-24T08:00:00Z","link":"https://e.x/a"}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/feed", strings.NewReader(body))
	// no X-Signature header

	rec := httptest.NewRecorder()
	wh.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestWebhook_ServeHTTP_HMAC_TamperedSig_Rejects(t *testing.T) {
	t.Parallel()
	wh := New(discardLogger())
	_ = wh.Start(t.Context(), make(chan *v1.Article, 1))
	wh.Register(newSignedSource("s1", "feed", "shh"))

	body := `{"title":"t","body":"x","ts":"2026-06-24T08:00:00Z","link":"https://e.x/a"}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/feed", strings.NewReader(body))
	req.Header.Set("X-Signature", "deadbeef")

	rec := httptest.NewRecorder()
	wh.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestWebhook_ServeHTTP_NoAuth_AcceptsPlainPayload(t *testing.T) {
	t.Parallel()
	out := make(chan *v1.Article, 1)
	wh := New(discardLogger())
	_ = wh.Start(t.Context(), out)
	// newSource has no Auth, so the verifier short-circuits.
	wh.Register(newSource("s1", "feed"))

	body := `{"title":"t","body":"some content","ts":"2026-06-24T08:00:00Z","link":"https://e.x/a"}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/feed", strings.NewReader(body))

	rec := httptest.NewRecorder()
	wh.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d body=%q, want 202", rec.Code, rec.Body.String())
	}
}

func TestWebhook_Deregister(t *testing.T) {
	t.Parallel()
	wh := New(discardLogger())
	_ = wh.Start(t.Context(), make(chan *v1.Article, 1))
	wh.Register(newSource("s1", "feed"))
	wh.Deregister("s1")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/feed", strings.NewReader(`{"body":"x"}`))
	rec := httptest.NewRecorder()
	wh.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("after Deregister status = %d, want 404", rec.Code)
	}
}

func TestWebhook_Register_WrongSpec_NoCrash(t *testing.T) {
	t.Parallel()
	wh := New(discardLogger())
	// RSS spec on a webhook source; Register should warn and skip.
	wh.Register(&v1.Source{ID: "bad", Type: v1.RSSType, Spec: &v1.RSSSpec{}})
}

func TestIntoArticle_TimestampFallsBackToNow(t *testing.T) {
	t.Parallel()
	src := newSource("s1", "p")
	before := time.Now()
	got, err := intoArticle(src, map[string]any{"body": "c"})
	after := time.Now()

	if err != nil {
		t.Fatalf("intoArticle err: %v", err)
	}
	if got.Timestamp.Before(before) || got.Timestamp.After(after) {
		t.Errorf("Timestamp %v not within [%v, %v]", got.Timestamp, before, after)
	}
}
