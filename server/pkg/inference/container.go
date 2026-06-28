package inference

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/notallthere404/futurecast/server/pkg/config"
)

// Container drives the local inference container via docker compose.
// The server holds one. Model + type swaps go through the inference
// service's own POST /load route (handled by the inference controller),
// so the container only handles container lifecycle (Stop on
// type=api transitions) and health polling.
//
// The compose context is the directory containing docker-compose.yml.
// Defaults to the CWD.
type Container struct {
	log        *slog.Logger
	composeDir string
	mu         sync.Mutex
}

const (
	defaultHealthPoll    = time.Second
	defaultHealthRequest = 2 * time.Second
)

// NewContainer returns an Container rooted at composeDir (the
// directory containing docker-compose.yml). Empty string defaults to
// the current working directory.
func NewContainer(log *slog.Logger, composeDir string) *Container {
	if composeDir == "" {
		composeDir = "."
	}
	return &Container{
		log:        log.With(slog.String("mod", "inference-container")),
		composeDir: composeDir,
	}
}

// Stop brings the inference service down. Use when switching to
// type=api so the container does not stay running unused.
func (o *Container) Stop(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.compose(ctx, "stop", "inference")
}

// compose runs `docker compose <args>` from the project dir, capturing
// stdout+stderr for diagnostics. Caller controls ctx for cancellation.
func (o *Container) compose(ctx context.Context, args ...string) error {
	full := append([]string{"compose"}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = o.composeDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		o.log.Error("docker compose failed",
			"args", args, "output", string(out), "error", err)
		return fmt.Errorf("docker compose %s: %w",
			strings.Join(args, " "), err)
	}

	o.log.Debug("docker compose ok", "args", args, "output", string(out))
	return nil
}

// WaitForHealth polls addr+/health on a fixed interval until /health
// returns 200, ctx is cancelled, or addr is empty. Caller controls the
// overall deadline via ctx; the inference controller's readiness
// goroutine passes the long-lived server ctx so a slow model download
// is not artificially capped here.
func (o *Container) WaitForHealth(ctx context.Context, addr string) error {
	if addr == "" {
		return errors.New("inference addr not set")
	}

	ticker := time.NewTicker(defaultHealthPoll)
	defer ticker.Stop()

	client := &http.Client{Timeout: defaultHealthRequest}
	url := strings.TrimRight(addr, "/") + "/health"

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			res, err := client.Get(url)
			if err == nil {
				_ = res.Body.Close()
				if res.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
	}
}

// InferenceChanged reports whether the fields that warrant a model
// reload (Type, Model, Engine) differ between two configs. Callers
// wire this into the config OnReload hook to avoid swapping the
// classifier on unrelated changes (e.g. classification label edits).
func InferenceChanged(prev, next *config.Config) bool {
	if prev == nil || next == nil {
		return true
	}
	return prev.Inference.Type != next.Inference.Type ||
		prev.Inference.Model != next.Inference.Model ||
		prev.Inference.Engine != next.Inference.Engine
}
