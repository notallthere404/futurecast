package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Manager owns the Viper instance, the live Config snapshot, and
// the subscriber list. Init returns one; the caller (config controller)
// holds it.
//
// cfg uses atomic.Pointer for lock-free reads. mu guards the callbacks
// slice only; do NOT hold it across user callbacks.
type Manager struct {
	v   *viper.Viper
	cfg atomic.Pointer[Config]

	mu        sync.RWMutex
	callbacks []func(*Config)
}

// Config is the top-level YAML shape. Each top-level key is its own
// typed sub-tree so parse errors point at the exact section that
// failed.
type Config struct {
	Server    Server       `mapstructure:"server"`
	Source    SourceConfig `mapstructure:"source"`
	Inference Inference    `mapstructure:"inference"`
}

// Server is the `server:` block — listen host/port, log level, and
// the optional external-database DSN (empty falls back to the
// DATABASE_URL env var).
type Server struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	LogLevel string `mapstructure:"log_level"`
	ExtDb    string `mapstructure:"ext_db"`
}

// Init call once at startup. Returned manager is held by the config
// controller; everything else accesses config through it.
func Init(path string) (*Manager, error) {
	cm := &Manager{
		v:         viper.New(),
		callbacks: make([]func(*Config), 0),
	}

	if path != "" {
		cm.v.SetConfigFile(path)
	} else {
		cm.v.SetConfigName("config")
		cm.v.SetConfigType("yml")
		cm.v.AddConfigPath(".")
	}

	cm.v.SetEnvPrefix("FTC")
	cm.v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	cm.v.AutomaticEnv()

	setDefaults(cm.v)

	if err := cm.load(); err != nil {
		return nil, err
	}

	// TODO(hot-reload): wire WatchConfig + OnConfigChange here when ready.
	//
	//   cm.v.WatchConfig()
	//   cm.v.OnConfigChange(func(e fsnotify.Event) {
	//       slog.Info("config changed", "file", e.Name)
	//       cm.reload()
	//   })
	//
	// reload() + notify() below are already wired so the only future change
	// is enabling the watcher. fsnotify caveat: editor "save via rename"

	return cm, nil
}

// load initial read. Missing file is fine (defaults + env may suffice).
func (cm *Manager) load() error {
	if err := cm.v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return fmt.Errorf("read config: %w", err)
		}
		slog.Info("no config file found; using defaults + env")
	}

	cfg, err := cm.parse()
	if err != nil {
		return err
	}
	cm.cfg.Store(cfg)
	return nil
}

// Reload explicit re-read + swap, triggered by the config controller
// after a manual write. Re-reads the file from disk, parses, validates,
// publishes via atomic swap, and fires subscribers. Errors keep the
// previous config in place.
func (cm *Manager) Reload() error {
	if err := cm.v.ReadInConfig(); err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	cfg, err := cm.parse()
	if err != nil {
		return err
	}
	cm.cfg.Store(cfg)
	cm.notify(cfg)
	return nil
}

// parse Unmarshal + Validate. Shared by load and reload so both paths
// have identical semantics.
func (cm *Manager) parse() (*Config, error) {
	var cfg Config
	if err := cm.v.Unmarshal(&cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		// viper's defaults; preserved when overriding DecodeHook.
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
		// project hooks.
		envExpandDecodeHook,
		labelStringDecodeHook,
	))); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return &cfg, nil
}

// Undefined vars expand to empty strings, matching os.ExpandEnv's
// semantics; treat that as a misconfiguration at the call site.
// Short-circuits on strings without a "$" so the common case skips
// the lookup overhead.
func envExpandDecodeHook(from, to reflect.Type, data any) (any, error) {
	if from.Kind() != reflect.String || to.Kind() != reflect.String {
		return data, nil
	}
	s, ok := data.(string)
	if !ok || !strings.Contains(s, "$") {
		return data, nil
	}
	return os.ExpandEnv(s), nil
}

// labelStringDecodeHook lets users write `labels: ["A", "B"]` as flat
// strings instead of the paired `[{name: A}, {name: B}]` table form.
func labelStringDecodeHook(from, to reflect.Type, data any) (any, error) {
	if to != reflect.TypeOf(InferenceLabel{}) {
		return data, nil
	}
	if from.Kind() != reflect.String {
		return data, nil
	}
	return map[string]any{"name": data}, nil
}

// Get is a lock-free read. Returned pointer is the live snapshot; treat as
// read-only (nested slices/maps share memory with the live config).
func (cm *Manager) Get() *Config {
	return cm.cfg.Load()
}

// OnReload registers a callback fired after each successful reload.
// Callbacks fire outside the lock so they may safely call back into Get.
func (cm *Manager) OnReload(cb func(*Config)) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.callbacks = append(cm.callbacks, cb)
}

func (cm *Manager) notify(cfg *Config) {
	// Snapshot the slice under the read lock, fire callbacks outside it.
	cm.mu.RLock()
	cbs := make([]func(*Config), len(cm.callbacks))
	copy(cbs, cm.callbacks)
	cm.mu.RUnlock()
	for _, cb := range cbs {
		cb(cfg)
	}
}

// Validate enforces required fields + value ranges. Fail closed on reload (keep
// prior config); fail open on initial load (Init returns the error).
func (c *Config) Validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}
	if err := c.Inference.validateAPI(); err != nil {
		return err
	}
	return c.Inference.validatePrompts()
}

// validateAPI ensures that type=api configs carry the credentials the
// Go-side remote dispatcher needs. Caught at config load so a missing
// key surfaces with a clear error instead of as per-article 401s.
func (i *Inference) validateAPI() error {
	if i.Type != InfTypeAPI {
		return nil
	}
	if i.Api == nil {
		return errors.New("inference.type=api requires inference.api.{endpoint, api_key}")
	}
	if i.Api.Endpoint == "" {
		return errors.New("inference.type=api requires inference.api.endpoint")
	}
	if i.Api.APIKey == "" {
		return errors.New("inference.type=api requires inference.api.api_key")
	}
	if i.Model == "" {
		return errors.New("inference.type=api requires inference.model (the remote model id)")
	}
	return nil
}

// validatePrompts checks every attribute's Prompt against the loaded
// inference Type. Catches the zero-shot vs LLM template mismatch at
// config-load time instead of as a 422 spam loop from the Python side.
//
// Rules:
//   - zeroshot: prompt must contain "{}" (HuggingFace hypothesis_template)
//   - llm:      prompt must contain "{label}"; "{definition}" optional
//   - api:      unvalidated (template semantics owned by the remote API)
//   - empty:    skipped (the inference service applies its own default)
func (i *Inference) validatePrompts() error {
	if i.Type == InfTypeAPI {
		return nil
	}
	for cls, attrs := range i.Classifications {
		for _, a := range attrs {
			if a.Prompt == "" {
				continue
			}
			switch i.Type {
			case InfTypeZeroshot:
				if !strings.Contains(a.Prompt, "{}") {
					return fmt.Errorf(
						"inference.%s[%s]: zeroshot prompt must contain {} placeholder, got %q",
						cls, a.Name, a.Prompt)
				}
			case InfTypeLlm:
				if !strings.Contains(a.Prompt, "{label}") {
					return fmt.Errorf(
						"inference.%s[%s]: llm prompt must contain {label} placeholder, got %q",
						cls, a.Name, a.Prompt)
				}
			}
		}
	}
	return nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.host", "localhost")
	v.SetDefault("server.port", 8765)
	v.SetDefault("server.log_level", "info")

	v.SetDefault("source.default.schedule", "*/10 * * * *")
	v.SetDefault("source.default.trust", "medium")

	v.SetDefault("inference.addr", "http://inference:8080")
	v.SetDefault("inference.engine", "transformers")
	v.SetDefault("inference.default.cutoff", 0.0)
	v.SetDefault("inference.default.top_n", 3)
}
