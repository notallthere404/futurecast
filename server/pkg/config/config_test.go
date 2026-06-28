package config

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// writeConfig drops a fixture file under dir and returns the path.
func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

const validYAML = `
server:
  host: "0.0.0.0"
  port: 9999
  log_level: debug
  ext_db: "postgres://user:pw@db:5432/x"

source:
  default:
    schedule: "0 * * * *"
    trust: high
`

func TestManager_EnvExpand_StringValues(t *testing.T) {
	// Uses t.Setenv; cannot t.Parallel.
	t.Setenv("TEST_SECRET", "shh-from-env")
	t.Setenv("TEST_BEARER", "bearer-from-env")

	yaml := `
server:
  host: "0.0.0.0"
  port: 9999

source:
  webhook:
    - name: Alerts
      path: /alerts
      active: true
      auth:
        kind: hmac
        header: X-Signature
        secret: ${TEST_SECRET}
  http:
    - name: Feed
      url: http://example.test/feed
      active: true
      auth:
        kind: bearer
        token: ${TEST_BEARER}
`
	dir := t.TempDir()
	path := writeConfig(t, dir, yaml)

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg := cm.Get()

	if len(cfg.Source.Webhook) != 1 || cfg.Source.Webhook[0].Auth == nil {
		t.Fatalf("webhook not loaded: %+v", cfg.Source.Webhook)
	}
	if got := cfg.Source.Webhook[0].Auth.Secret; got != "shh-from-env" {
		t.Errorf("webhook secret = %q, want expanded env value", got)
	}

	if len(cfg.Source.HTTP) != 1 || cfg.Source.HTTP[0].Auth == nil {
		t.Fatalf("http not loaded: %+v", cfg.Source.HTTP)
	}
	if got := cfg.Source.HTTP[0].Auth.Token; got != "bearer-from-env" {
		t.Errorf("http token = %q, want expanded env value", got)
	}
}

func TestManager_EnvExpand_UndefinedVarBecomesEmpty(t *testing.T) {
	// Uses t.Setenv; cannot t.Parallel.
	t.Setenv("DEFINED", "yes")
	// DELIBERATELY_UNSET is not set in the env.

	yaml := `
server:
  host: "0.0.0.0"
  port: 9999

source:
  webhook:
    - name: Alerts
      path: /alerts
      active: true
      auth:
        kind: hmac
        secret: ${DELIBERATELY_UNSET}
`
	dir := t.TempDir()
	path := writeConfig(t, dir, yaml)

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg := cm.Get()
	if got := cfg.Source.Webhook[0].Auth.Secret; got != "" {
		t.Errorf("undefined var = %q, want empty string", got)
	}
}

func TestManager_EnvExpand_LiteralWithoutDollarUntouched(t *testing.T) {
	t.Parallel()
	yaml := `
server:
  host: "0.0.0.0"
  port: 9999

source:
  webhook:
    - name: Alerts
      path: /alerts
      active: true
      auth:
        kind: hmac
        secret: literal-no-substitution
`
	dir := t.TempDir()
	path := writeConfig(t, dir, yaml)

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg := cm.Get()
	if got := cfg.Source.Webhook[0].Auth.Secret; got != "literal-no-substitution" {
		t.Errorf("literal value = %q, want unchanged", got)
	}
}

func TestManager_LoadValid(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, t.TempDir(), validYAML)

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	cfg := cm.Get()
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("host: want 0.0.0.0, got %q", cfg.Server.Host)
	}
	if cfg.Server.Port != 9999 {
		t.Errorf("port: want 9999, got %d", cfg.Server.Port)
	}
	if cfg.Server.LogLevel != "debug" {
		t.Errorf("log_level: want debug, got %q", cfg.Server.LogLevel)
	}
	if cfg.Server.ExtDb != "postgres://user:pw@db:5432/x" {
		t.Errorf("ext_db: got %q", cfg.Server.ExtDb)
	}
	if cfg.Source.Default.Schedule != "0 * * * *" {
		t.Errorf("source.schedule: got %q", cfg.Source.Default.Schedule)
	}
	if cfg.Source.Default.Trust != "high" {
		t.Errorf("source.trust: got %q", cfg.Source.Default.Trust)
	}
}

func TestManager_LoadMissingFile(t *testing.T) {
	// Uses t.Setenv; cannot t.Parallel.

	// Force auto-search mode to find nothing: chdir to an empty
	// directory so AddConfigPath(".") matches no file.
	t.Chdir(t.TempDir())

	cm, err := Init("")
	if err != nil {
		t.Fatalf("Init with missing file should succeed, got: %v", err)
	}

	cfg := cm.Get()
	// Defaults must apply when no file is found.
	if cfg.Server.Host != "localhost" {
		t.Errorf("default host not applied: got %q", cfg.Server.Host)
	}
	if cfg.Server.Port != 8765 {
		t.Errorf("default port not applied: got %d", cfg.Server.Port)
	}
}

func TestManager_LoadMalformed(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, t.TempDir(), `
server:
  port: this is not a number
  : :: bad yaml
`)

	if _, err := Init(path); err == nil {
		t.Fatal("expected error from malformed YAML, got nil")
	}
}

func TestManager_LoadInvalidPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		port int
	}{
		{"port zero", 0},
		{"port below range", -1},
		{"port above range", 70000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, t.TempDir(),
				"server:\n  port: "+itoa(tc.port)+"\n")
			if _, err := Init(path); err == nil {
				t.Fatalf("port %d: expected validate error, got nil", tc.port)
			}
		})
	}
}

func TestManager_DefaultsApplied(t *testing.T) {
	t.Parallel()

	// Minimal file overriding nothing; every key must come from defaults.
	path := writeConfig(t, t.TempDir(), `# empty config
`)

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	cfg := cm.Get()
	if cfg.Server.Host != "localhost" {
		t.Errorf("server.host default: got %q", cfg.Server.Host)
	}
	if cfg.Server.Port != 8765 {
		t.Errorf("server.port default: got %d", cfg.Server.Port)
	}
	if cfg.Server.LogLevel != "info" {
		t.Errorf("server.log_level default: got %q", cfg.Server.LogLevel)
	}
	if cfg.Source.Default.Schedule != "*/10 * * * *" {
		t.Errorf("source.schedule default: got %q", cfg.Source.Default.Schedule)
	}
	if cfg.Source.Default.Trust != "medium" {
		t.Errorf("source.trust default: got %q", cfg.Source.Default.Trust)
	}
	if cfg.Inference.Default.TopN != 3 {
		t.Errorf("inference.top_n default: got %d", cfg.Inference.Default.TopN)
	}
}

func TestManager_EnvOverride(t *testing.T) {
	// Uses t.Setenv; no t.Parallel.
	t.Setenv("FTC_SERVER_PORT", "9000")

	path := writeConfig(t, t.TempDir(), validYAML) // file says 9999

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	if got := cm.Get().Server.Port; got != 9000 {
		t.Errorf("env should beat file: want 9000, got %d", got)
	}
}

func TestManager_EnvKeyReplacer(t *testing.T) {
	// Uses t.Setenv; no t.Parallel.
	// Verifies dots-to-underscores translation: source.default.schedule
	// via env var FTC_SOURCE_DEFAULT_SCHEDULE.
	t.Setenv("FTC_SOURCE_DEFAULT_SCHEDULE", "*/2 * * * *")

	path := writeConfig(t, t.TempDir(), `
server:
  port: 8765
`)

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	if got := cm.Get().Source.Default.Schedule; got != "*/2 * * * *" {
		t.Errorf("env nested key: want %q, got %q", "*/2 * * * *", got)
	}
}

func TestManager_ReloadSuccess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeConfig(t, dir, validYAML) // port 9999

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if cm.Get().Server.Port != 9999 {
		t.Fatalf("initial port should be 9999")
	}

	// Overwrite with new content.
	writeConfig(t, dir, `
server:
  port: 8800
`)

	if err := cm.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := cm.Get().Server.Port; got != 8800 {
		t.Errorf("after reload: want 8800, got %d", got)
	}
}

func TestManager_ReloadBadYamlKeepsPrior(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeConfig(t, dir, validYAML) // port 9999

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	writeConfig(t, dir, `not: valid: yaml: at all:`)

	if err := cm.Reload(); err == nil {
		t.Fatal("Reload with malformed YAML should error")
	}
	if got := cm.Get().Server.Port; got != 9999 {
		t.Errorf("prior config should remain after failed reload: want 9999, got %d", got)
	}
}

func TestManager_ReloadInvalidPortKeepsPrior(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeConfig(t, dir, validYAML) // port 9999

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Write parses cleanly but fails validation.
	writeConfig(t, dir, `
server:
  port: 70000
`)

	if err := cm.Reload(); err == nil {
		t.Fatal("Reload with out-of-range port should error")
	}
	if got := cm.Get().Server.Port; got != 9999 {
		t.Errorf("prior config should remain after validate failure: want 9999, got %d", got)
	}
	_ = path
}

func TestManager_OnReloadFiresOnSuccess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeConfig(t, dir, validYAML)

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	var got atomic.Int64
	var received *Config
	var mu sync.Mutex

	cm.OnReload(func(cfg *Config) {
		got.Add(1)
		mu.Lock()
		received = cfg
		mu.Unlock()
	})

	writeConfig(t, dir, `
server:
  port: 8800
`)

	if err := cm.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if n := got.Load(); n != 1 {
		t.Errorf("callback fire count: want 1, got %d", n)
	}

	mu.Lock()
	defer mu.Unlock()
	if received == nil || received.Server.Port != 8800 {
		t.Errorf("callback received wrong config: %+v", received)
	}
}

func TestManager_OnReloadDoesNotFireOnFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeConfig(t, dir, validYAML)

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	var got atomic.Int64
	cm.OnReload(func(*Config) { got.Add(1) })

	writeConfig(t, dir, `not: valid: yaml:`)

	if err := cm.Reload(); err == nil {
		t.Fatal("expected reload error")
	}

	if n := got.Load(); n != 0 {
		t.Errorf("callback must not fire on failed reload, fired %d times", n)
	}
}

func TestManager_GetReturnsLatest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeConfig(t, dir, validYAML)

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	first := cm.Get()

	writeConfig(t, dir, `
server:
  port: 8800
`)
	if err := cm.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	second := cm.Get()

	if first == second {
		t.Error("Get returned identical pointer before and after reload")
	}
	if cmp.Equal(first, second) {
		t.Error("Get returned equal value before and after reload")
	}
}

//
// Run with -race. The atomic.Pointer-backed cfg field makes Get lock-free,
// and notify snapshots the callbacks slice under RLock before invoking
// them so a concurrent OnReload registration cannot race the notify loop.
// This test exercises both paths.

func TestManager_ConcurrentGetReload(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeConfig(t, dir, validYAML)

	cm, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	var callbackHits atomic.Int64
	cm.OnReload(func(*Config) { callbackHits.Add(1) })

	const (
		readers          = 4
		readsPerReader   = 5000
		reloadIterations = 100
	)

	var wg sync.WaitGroup

	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range readsPerReader {
				cfg := cm.Get()
				if cfg == nil {
					t.Errorf("Get returned nil")
					return
				}
				// Touch a field so the read isn't optimised away.
				_ = cfg.Server.Port
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range reloadIterations {
			_ = cm.Reload()
		}
	}()

	// Spawn a goroutine registering more callbacks while notify may fire.
	// Exercises the subs-map lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 50 {
			cm.OnReload(func(*Config) { callbackHits.Add(1) })
		}
	}()

	wg.Wait()

	// Sanity: at least the original callback fired on each reload.
	if got := callbackHits.Load(); got < int64(reloadIterations) {
		t.Errorf("callback count too low: got %d, want >= %d", got, reloadIterations)
	}
}

// itoa local tiny helper to avoid pulling strconv just for fixture text.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
