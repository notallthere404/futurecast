package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
	"github.com/notallthere404/futurecast/server/pkg/utils"
)

// Webhook implements source.Listener for inbound HTTP webhooks. Each
// active webhook source registers its path; ServeHTTP dispatches
// requests by path and writes parsed articles to the out channel.
type Webhook struct {
	log    *slog.Logger
	mu     sync.RWMutex
	routes map[string]*v1.Source
	out    chan<- *v1.Article
}

// New returns an empty Webhook listener. Sources bind via Register
// once the system controller has loaded the active set.
func New(log *slog.Logger) *Webhook {
	return &Webhook{
		log:    log.With(slog.String("source", "webhook")),
		routes: make(map[string]*v1.Source),
	}
}

// Kind reports which SourceType this driver handles.
func (wh *Webhook) Kind() v1.SourceType { return v1.WebhookType }

// Start captures the article channel. Webhook is request-driven,
// no long-running goroutine to spawn here.
func (wh *Webhook) Start(_ context.Context, out chan<- *v1.Article) error {
	wh.out = out
	return nil
}

// Stop is a no-op. Per-request handler holds no resources.
func (wh *Webhook) Stop() {}

// Register binds an active source to its webhook path. The path is
// normalised (leading slash stripped) so config can write either
// "alerts" or "/alerts" and ServeHTTP still matches.
func (wh *Webhook) Register(src *v1.Source) {
	spec := src.Webhook()
	if spec == nil {
		wh.log.Warn("register: wrong spec type", "id", src.ID)
		return
	}
	wh.mu.Lock()
	defer wh.mu.Unlock()
	wh.routes[normalisePath(spec.Path)] = src
}

// normalisePath drops a leading "/" so registered and request paths
// share the same canonical form.
func normalisePath(p string) string {
	return strings.TrimPrefix(p, "/")
}

// Deregister drops a binding by source id (path lookup is reverse).
func (wh *Webhook) Deregister(srcID string) {
	wh.mu.Lock()
	defer wh.mu.Unlock()
	for path, src := range wh.routes {
		if src.ID == srcID {
			delete(wh.routes, path)
			return
		}
	}
}

// ServeHTTP is mounted at /webhooks/ by the app server. Dispatches by
// suffix path. Reads the body once so the HMAC verifier and the JSON
// decoder both see the same bytes; a streaming Decode would consume
// the body before the signature could be checked.
func (wh *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/webhooks/")
	wh.mu.RLock()
	src, ok := wh.routes[path]
	wh.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	if status, msg := verifyAuth(src.Auth, r.Header, body); status != 0 {
		wh.log.Warn("webhook auth rejected",
			"source", src.Name, "status", status, "reason", msg)
		http.Error(w, msg, status)
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	article, err := intoArticle(src, payload)
	if err != nil {
		wh.log.Error("could not extract article", "error", err)
		http.Error(w, "could not parse article", http.StatusBadRequest)
		return
	}

	wh.out <- article
	w.WriteHeader(http.StatusAccepted)
}

// verifyAuth checks the inbound request against the source's auth
// config. Returns (0, "") on success; otherwise an HTTP status + reason
// the handler should send back. Currently handles kind=hmac (SHA-256
// over the raw body, hex-encoded, constant-time compare). Unknown
// kinds and a nil Auth fall through as a no-op so sources without
// auth keep working.
func verifyAuth(auth *v1.Auth, headers http.Header, body []byte) (int, string) {
	if auth == nil {
		return 0, ""
	}
	switch auth.Kind {
	case "hmac":
		header := auth.Header
		if header == "" {
			header = "X-Signature"
		}
		got := headers.Get(header)
		if got == "" {
			return http.StatusUnauthorized, "missing signature"
		}
		mac := hmac.New(sha256.New, []byte(auth.Secret))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(want), []byte(got)) {
			return http.StatusForbidden, "invalid signature"
		}
		return 0, ""
	}
	return 0, ""
}

func intoArticle(src *v1.Source, item map[string]any) (*v1.Article, error) {
	title, _ := v1.ExtractString(src.Extract.Title, item)
	content, ok := v1.ExtractString(src.Extract.Content, item)
	if !ok {
		return nil, errors.New("content field could not be extracted")
	}
	if content == "" {
		return nil, errors.New("content field is empty")
	}

	ts, ok := v1.ExtractTime(src.Extract.Timestamp, item)
	if !ok {
		ts = time.Now()
	}

	link, _ := v1.ExtractString(src.Extract.Link, item)

	return &v1.Article{
		ID:         utils.NewArticleID(link),
		SourceID:   src.ID,
		SourceType: v1.WebhookType,
		Title:      title,
		Content:    content,
		Timestamp:  ts,
		Link:       link,
		Processed:  false,
	}, nil
}
