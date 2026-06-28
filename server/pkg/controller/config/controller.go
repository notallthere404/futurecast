package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	cfgpkg "github.com/notallthere404/futurecast/server/pkg/config"
)

// Controller wraps the config package's Manager and exposes the slim
// surface other controllers depend on (Get, OnReload) plus the
// dashboard read/write (Raw, Write, ClientConfig).
type Controller struct {
	log  *slog.Logger
	cm   *cfgpkg.Manager
	path string
}

// New loads the config at path and returns a controller. An empty
// path falls back to viper's search defaults (see cfgpkg.Init).
func New(log *slog.Logger, path string) (*Controller, error) {
	cm, err := cfgpkg.Init(path)
	if err != nil {
		return nil, err
	}

	return &Controller{
		log:  log.With(slog.String("controller", "config")),
		cm:   cm,
		path: path,
	}, nil
}

// Get returns the current config snapshot. Callers treat the returned
// pointer as read-only; nested slices and maps share memory with the
// live config.
func (c *Controller) Get() *cfgpkg.Config {
	return c.cm.Get()
}

// OnReload exposes the manager's subscription seam so other controllers
// can react to hot-reload events without holding the manager directly.
func (c *Controller) OnReload(cb func(*cfgpkg.Config)) {
	c.cm.OnReload(cb)
}

// Raw returns the raw YAML source as last written to disk. Used by the
// dashboard config editor.
func (c *Controller) Raw() (string, error) {
	if c.path == "" {
		return "", nil
	}
	b, err := os.ReadFile(c.path)
	if err != nil {
		return "", fmt.Errorf("read config file: %w", err)
	}
	return string(b), nil
}

// Write replaces the config file on disk with raw and triggers a
// reload. Once WatchConfig is enabled this becomes redundant (fsnotify
// will fire on its own); keeping it explicit makes dashboard updates
// deterministic regardless of file-watch state.
func (c *Controller) Write(raw string) error {
	if c.path == "" {
		return errors.New("config path not set")
	}
	if err := os.WriteFile(c.path, []byte(raw), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return c.cm.Reload()
}

// ClientConfig returns the dashboard payload: raw YAML + the
// classification-to-attribute map.
func (c *Controller) ClientConfig() (cfgpkg.ClientConfig, error) {
	raw, err := c.Raw()
	if err != nil {
		return cfgpkg.ClientConfig{}, err
	}
	return c.Get().ToClientConfig(raw), nil
}
