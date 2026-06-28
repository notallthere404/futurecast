package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	cfgpkg "github.com/notallthere404/futurecast/server/pkg/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

const baseYAML = `
server:
  host: 127.0.0.1
  port: 8080
  log_level: info
source:
  default:
    schedule: "*/10 * * * *"
inference:
  addr: http://inference:8080
  mode: queue
  model: dummy
  default:
    cutoff: 0.5
    top_n: 1
  events:
    - name: vector
      prompt: "topic: {}"
      labels:
        - name: alpha
        - name: beta
`

func writeTmp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	return path
}

func TestNew_ValidConfig_GetReturnsParsed(t *testing.T) {
	t.Parallel()
	path := writeTmp(t, baseYAML)
	ctrl, err := New(discardLogger(), path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := ctrl.Get()
	if got.Server.Host != "127.0.0.1" || got.Server.Port != 8080 {
		t.Errorf("server fields = %+v", got.Server)
	}
	if got.Inference.Model != "dummy" {
		t.Errorf("Inference.Model = %q, want dummy", got.Inference.Model)
	}
}

func TestNew_InvalidConfig_Errors(t *testing.T) {
	t.Parallel()
	path := writeTmp(t, "not: [valid: yaml")
	if _, err := New(discardLogger(), path); err == nil {
		t.Error("expected parse error on broken YAML")
	}
}

func TestRaw_ReturnsFileContents(t *testing.T) {
	t.Parallel()
	path := writeTmp(t, baseYAML)
	ctrl, err := New(discardLogger(), path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := ctrl.Raw()
	if err != nil {
		t.Fatalf("Raw: %v", err)
	}
	if got != baseYAML {
		t.Errorf("Raw mismatch:\ngot:\n%s\nwant:\n%s", got, baseYAML)
	}
}

func TestRaw_EmptyPathReturnsEmpty(t *testing.T) {
	// New with path="" looks for a default-located config. We can't
	// exercise that without polluting $HOME, so we only test the path
	// guard Raw() applies when path is empty.
	t.Parallel()
	ctrl := &Controller{path: ""}
	got, err := ctrl.Raw()
	if err != nil {
		t.Fatalf("Raw err: %v", err)
	}
	if got != "" {
		t.Errorf("empty path Raw = %q, want \"\"", got)
	}
}

func TestWrite_PersistsAndReloads(t *testing.T) {
	// Write replaces the file then forces a Reload; Get must see the
	// new values immediately. This is the path the dashboard's config
	// editor hits when the user saves.
	t.Parallel()
	path := writeTmp(t, baseYAML)
	ctrl, err := New(discardLogger(), path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	updated := `
server:
  host: 0.0.0.0
  port: 9090
  log_level: debug
source:
  default:
    schedule: "0 * * * *"
inference:
  addr: http://inference:8080
  mode: queue
  model: bigger
  default:
    cutoff: 0.7
    top_n: 1
  events:
    - name: vector
      prompt: "topic: {}"
      labels:
        - name: alpha
`
	if err := ctrl.Write(updated); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := ctrl.Get()
	if got.Server.Port != 9090 || got.Inference.Model != "bigger" {
		t.Errorf("config not refreshed: %+v / model=%q", got.Server, got.Inference.Model)
	}

	raw, _ := ctrl.Raw()
	if raw != updated {
		t.Errorf("Raw did not reflect Write")
	}
}

func TestWrite_EmptyPath_Errors(t *testing.T) {
	t.Parallel()
	ctrl := &Controller{path: ""}
	if err := ctrl.Write("anything"); err == nil {
		t.Error("expected error when writing without a path")
	}
}

func TestWrite_InvalidYAMLBubblesParseError(t *testing.T) {
	// A failed reload after a bad Write should bubble the parse error
	// so the handler can surface it; the on-disk file was still
	// updated (caller's responsibility to recover).
	t.Parallel()
	path := writeTmp(t, baseYAML)
	ctrl, err := New(discardLogger(), path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := ctrl.Write("not: [valid: yaml"); err == nil {
		t.Error("expected reload error from invalid YAML")
	}
}

func TestOnReload_SubscriberFiresOnWrite(t *testing.T) {
	t.Parallel()
	path := writeTmp(t, baseYAML)
	ctrl, err := New(discardLogger(), path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var called atomic.Int32
	var lastModel atomic.Value
	ctrl.OnReload(func(c *cfgpkg.Config) {
		called.Add(1)
		lastModel.Store(c.Inference.Model)
	})

	updated := baseYAML + "\n# tweak to trigger reload\n"
	if err := ctrl.Write(updated); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if called.Load() != 1 {
		t.Errorf("subscriber called %d times, want 1", called.Load())
	}
	if got, _ := lastModel.Load().(string); got != "dummy" {
		t.Errorf("subscriber saw model %q, want dummy", got)
	}
}

func TestClientConfig_ContainsRawAndClassMap(t *testing.T) {
	t.Parallel()
	path := writeTmp(t, baseYAML)
	ctrl, err := New(discardLogger(), path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cc, err := ctrl.ClientConfig()
	if err != nil {
		t.Fatalf("ClientConfig: %v", err)
	}
	if cc.Raw != baseYAML {
		t.Errorf("ClientConfig.Raw mismatch")
	}
	if _, ok := cc.ClassMap["events"]; !ok {
		t.Errorf("ClientConfig.ClassMap missing 'events': %+v", cc.ClassMap)
	}
}
