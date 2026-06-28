package inference

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/notallthere404/futurecast/server/pkg/inference"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
	cfgpkg "github.com/notallthere404/futurecast/server/pkg/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// ────────────────────────── fakes ──────────────────────────//

type fakeClient struct {
	mu            sync.Mutex
	target        inference.Target
	infoOut       v1.InferenceInfo
	infoErr       error
	infoCalls     atomic.Int32
	loadCalls     atomic.Int32
	loadModel     string
	loadType      string
	loadErr       error
	classifyOut   map[string][]*inference.ClassifyResponse // keyed by article ID
	classifyErr   error
	classifyCalls atomic.Int32
}

func (f *fakeClient) SetTarget(t inference.Target) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.target = t
}

func (f *fakeClient) Target() inference.Target {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.target
}

func (f *fakeClient) Info(context.Context) (v1.InferenceInfo, error) {
	f.infoCalls.Add(1)
	return f.infoOut, f.infoErr
}

func (f *fakeClient) Load(_ context.Context, model, typ string) error {
	f.loadCalls.Add(1)
	f.mu.Lock()
	f.loadModel = model
	f.loadType = typ
	f.mu.Unlock()
	return f.loadErr
}

func (f *fakeClient) Classify(_ context.Context, art *v1.ClassifyArticle, _ []v1.ClassificationSpec) ([]*inference.ClassifyResponse, error) {
	f.classifyCalls.Add(1)
	if f.classifyErr != nil {
		return nil, f.classifyErr
	}
	if rsp, ok := f.classifyOut[art.ID]; ok {
		return rsp, nil
	}
	return nil, nil
}

type fakeContainer struct {
	stopErr     error
	healthErr   error
	stopCalls   atomic.Int32
	healthCalls atomic.Int32
}

func (f *fakeContainer) Stop(context.Context) error { f.stopCalls.Add(1); return f.stopErr }
func (f *fakeContainer) WaitForHealth(_ context.Context, _ string) error {
	f.healthCalls.Add(1)
	return f.healthErr
}

type fakeMode struct {
	autoDrive    bool
	refillOut    []*v1.ClassifyArticle
	refillErr    error
	refillCalls  atomic.Int32
	persistErr   error
	persistCalls atomic.Int32
	persisted    v1.MappedClassArray
	mu           sync.Mutex
}

func (m *fakeMode) AutoDrive() bool { return m.autoDrive }
func (m *fakeMode) Refill(_ context.Context, _ int) ([]*v1.ClassifyArticle, error) {
	if m.refillCalls.Add(1) > 1 {
		// Only deliver the batch on first call so the loop drains and exits.
		return nil, nil
	}
	return m.refillOut, m.refillErr
}

func (m *fakeMode) Persist(_ context.Context, results v1.MappedClassArray) error {
	m.persistCalls.Add(1)
	m.mu.Lock()
	m.persisted = results
	m.mu.Unlock()
	return m.persistErr
}

type fakeConfig struct {
	mu        sync.Mutex
	current   *cfgpkg.Config
	callbacks []func(*cfgpkg.Config)
}

func newFakeConfig(cfg *cfgpkg.Config) *fakeConfig {
	return &fakeConfig{current: cfg}
}

func (f *fakeConfig) Get() *cfgpkg.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}

func (f *fakeConfig) OnReload(cb func(*cfgpkg.Config)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callbacks = append(f.callbacks, cb)
}

func (f *fakeConfig) trigger(cfg *cfgpkg.Config) {
	f.mu.Lock()
	f.current = cfg
	cbs := append([]func(*cfgpkg.Config){}, f.callbacks...)
	f.mu.Unlock()
	for _, cb := range cbs {
		cb(cfg)
	}
}

func baseConfig() *cfgpkg.Config {
	return &cfgpkg.Config{
		Inference: cfgpkg.Inference{
			Addr:   "http://inference:8080",
			Mode:   "continuous",
			Model:  "small",
			Engine: "transformers",
			Type:   "zeroshot",
		},
	}
}

func newCtrl(cli *fakeClient, cont *fakeContainer, cfg *fakeConfig, mode *fakeMode) *Controller {
	if cli == nil {
		cli = &fakeClient{}
	}
	if cont == nil {
		cont = &fakeContainer{}
	}
	if cfg == nil {
		cfg = newFakeConfig(baseConfig())
	}
	if mode == nil {
		mode = &fakeMode{autoDrive: true}
	}
	return New(discardLogger(), cfg, cli, cont, mode)
}

// ────────────────────────── tests ──────────────────────────//

func TestKick_NotReady_NoOp(t *testing.T) {
	t.Parallel()
	cli := &fakeClient{}
	mode := &fakeMode{autoDrive: true}
	ctrl := newCtrl(cli, nil, nil, mode)

	ctrl.Kick()
	time.Sleep(20 * time.Millisecond)
	if got := mode.refillCalls.Load(); got != 0 {
		t.Errorf("kick before ready should not refill, got %d refill calls", got)
	}
}

func TestKick_Ready_ContinuousMode_FiresLoop(t *testing.T) {
	t.Parallel()
	cli := &fakeClient{}
	mode := &fakeMode{
		autoDrive: true,
		refillOut: []*v1.ClassifyArticle{{ID: "a"}},
	}
	ctrl := newCtrl(cli, nil, nil, mode)
	ctrl.ready.Store(true)

	ctrl.Kick()

	// Wait for loop to drain.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !ctrl.loopRunning() && mode.refillCalls.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if mode.refillCalls.Load() < 1 {
		t.Errorf("refill calls = %d, want >=1", mode.refillCalls.Load())
	}
	if cli.classifyCalls.Load() != 1 {
		t.Errorf("classify calls = %d, want 1", cli.classifyCalls.Load())
	}
}

func TestKick_Ready_ManualMode_NoOp(t *testing.T) {
	t.Parallel()
	cli := &fakeClient{}
	mode := &fakeMode{autoDrive: false}
	ctrl := newCtrl(cli, nil, nil, mode)
	ctrl.ready.Store(true)

	ctrl.Kick()
	time.Sleep(20 * time.Millisecond)
	if got := mode.refillCalls.Load(); got != 0 {
		t.Errorf("manual mode kick should not refill, got %d", got)
	}
}

func TestClassifyInline_RunsAndPersists(t *testing.T) {
	t.Parallel()
	art := &v1.ClassifyArticle{ID: "00000000-0000-0000-0000-000000000001", Content: "x", Timestamp: time.Now()}
	cli := &fakeClient{
		classifyOut: map[string][]*inference.ClassifyResponse{
			art.ID: {{
				Classification: "events",
				ID:             "00000000-0000-0000-0000-000000000002",
				ArticleID:      art.ID,
				Timestamp:      time.Now().Format(time.RFC3339),
				Data: map[string][]*v1.LabelScore{
					"tactic": {{Label: "Reconnaissance", Score: 0.9}},
				},
			}},
		},
	}
	mode := &fakeMode{autoDrive: false}
	// Manual mode + ClassifyInline doesn't need a real spec, but the
	// controller reads from config for non-empty validation.
	cfg := newFakeConfig(baseConfig())
	cfg.current.Inference.Classifications = map[string][]cfgpkg.InferenceAttribute{
		"events": {{Name: "tactic", Labels: []cfgpkg.InferenceLabel{{Name: "Reconnaissance"}}}},
	}
	ctrl := newCtrl(cli, nil, cfg, mode)

	out, err := ctrl.ClassifyInline(context.Background(), []*v1.ClassifyArticle{art})
	if err != nil {
		t.Fatalf("ClassifyInline: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("got %d responses, want 1", len(out))
	}
	if mode.persistCalls.Load() != 1 {
		t.Errorf("persist calls = %d, want 1", mode.persistCalls.Load())
	}
}

func TestOnReload_LoadsOnlyWhenInferenceChanged(t *testing.T) {
	t.Parallel()
	cli := &fakeClient{}
	cont := &fakeContainer{}
	cfg := newFakeConfig(baseConfig())
	_ = newCtrl(cli, cont, cfg, &fakeMode{autoDrive: true})

	cfg.trigger(baseConfig())
	time.Sleep(50 * time.Millisecond)
	if cli.loadCalls.Load() != 0 {
		t.Fatalf("identical reload should not load, got %d", cli.loadCalls.Load())
	}

	changed := baseConfig()
	changed.Inference.Model = "bigger"
	cfg.trigger(changed)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cli.loadCalls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cli.loadCalls.Load() != 1 {
		t.Errorf("changed reload loads = %d, want 1", cli.loadCalls.Load())
	}
	cli.mu.Lock()
	defer cli.mu.Unlock()
	if cli.loadModel != "bigger" {
		t.Errorf("load model = %q, want bigger", cli.loadModel)
	}
}

func TestStop_DelegatesToContainer(t *testing.T) {
	t.Parallel()
	cont := &fakeContainer{}
	ctrl := newCtrl(nil, cont, nil, nil)
	if err := ctrl.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if cont.stopCalls.Load() != 1 {
		t.Errorf("container.Stop calls = %d, want 1", cont.stopCalls.Load())
	}
}

func TestSyncToConfig_APIMode_SkipsHealthProbe(t *testing.T) {
	t.Parallel()
	cli := &fakeClient{}
	cont := &fakeContainer{}
	cfg := baseConfig()
	cfg.Inference.Type = "api"
	cfg.Inference.Api = &cfgpkg.InferenceAPI{Endpoint: "https://x", APIKey: "k"}
	cfgFake := newFakeConfig(cfg)
	ctrl := newCtrl(cli, cont, cfgFake, &fakeMode{autoDrive: false})

	if err := ctrl.syncToConfig(context.Background()); err != nil {
		t.Fatalf("syncToConfig: %v", err)
	}
	if cont.healthCalls.Load() != 0 {
		t.Errorf("api mode must not probe health, got %d calls", cont.healthCalls.Load())
	}
	if got := cli.Target(); got.Type != "api" || got.Endpoint != "https://x" || got.APIKey != "k" {
		t.Errorf("target = %+v", got)
	}
}

// silence unused.
var _ = errors.New
