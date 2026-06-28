package system

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/notallthere404/futurecast/server/pkg/config"
	"github.com/notallthere404/futurecast/server/pkg/schema"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

type fakeSourceStore struct {
	all          []*v1.Source
	allErr       error
	deletedURLs  []string
	deletedCalls int
	upsertCalls  [][]*v1.Source
	deleteErr    error
	upsertErr    error
}

func (f *fakeSourceStore) SelectSourceAll(context.Context) ([]*v1.Source, error) {
	return f.all, f.allErr
}

func (f *fakeSourceStore) DeleteSourceBatch(_ context.Context, urls []string) error {
	f.deletedCalls++
	f.deletedURLs = append(f.deletedURLs, urls...)
	return f.deleteErr
}

func (f *fakeSourceStore) UpsertSourceBatch(_ context.Context, srcs []*v1.Source) error {
	f.upsertCalls = append(f.upsertCalls, srcs)
	return f.upsertErr
}

type fakeClassificationStore struct {
	tables       map[string]struct{}
	tablesErr    error
	keys         map[string]map[string]struct{} // table-to-keys
	createdCalls []string
	createErr    error
	droppedCalls []string
	dropErr      error
	updatedCalls []struct {
		table       string
		add, remove []string
	}
	updateErr error

	mu       sync.Mutex
	rLockObs func() // optional probe invoked while inside CreateTable
}

func (f *fakeClassificationStore) SelectTableAll(context.Context) (map[string]struct{}, error) {
	return f.tables, f.tablesErr
}

func (f *fakeClassificationStore) SelectDataKeyAll(_ context.Context, table string) (map[string]struct{}, error) {
	return f.keys[table], nil
}

func (f *fakeClassificationStore) UpdateDataKeyAll(_ context.Context, table string, add, remove []string) error {
	f.mu.Lock()
	f.updatedCalls = append(f.updatedCalls, struct {
		table       string
		add, remove []string
	}{table, add, remove})
	f.mu.Unlock()
	return f.updateErr
}

func (f *fakeClassificationStore) CreateTable(_ context.Context, table string) error {
	f.mu.Lock()
	f.createdCalls = append(f.createdCalls, table)
	f.mu.Unlock()
	if f.rLockObs != nil {
		f.rLockObs()
	}
	return f.createErr
}

func (f *fakeClassificationStore) DropTable(_ context.Context, table string) error {
	f.mu.Lock()
	f.droppedCalls = append(f.droppedCalls, table)
	f.mu.Unlock()
	return f.dropErr
}

type fakeMonitorStore struct {
	uptime      int
	uptimeErr   error
	total       float64
	totalErr    error
	totalArgs   []struct{ start, end string }
	segment     []float64
	segmentErr  error
	segmentArgs []v1.RateFormat
}

func (f *fakeMonitorStore) UpsertUptimeEntry(_ context.Context, _ int) (int, error) {
	return f.uptime, f.uptimeErr
}

func (f *fakeMonitorStore) SelectUptimeTotal(_ context.Context, start, end string) (float64, error) {
	f.totalArgs = append(f.totalArgs, struct{ start, end string }{start, end})
	return f.total, f.totalErr
}

func (f *fakeMonitorStore) SelectUptimeSegment(_ context.Context, format v1.RateFormat) ([]float64, error) {
	f.segmentArgs = append(f.segmentArgs, format)
	return f.segment, f.segmentErr
}

type fakeScheduler struct {
	stopCalls int
	runCalls  int
	addErr    error
	addCalls  []struct {
		label string
		expr  string
	}
}

func (f *fakeScheduler) Run()  { f.runCalls++ }
func (f *fakeScheduler) Stop() { f.stopCalls++ }
func (f *fakeScheduler) Add(label, expr string, _ func(context.Context) error) error {
	f.addCalls = append(f.addCalls, struct {
		label string
		expr  string
	}{label, expr})
	return f.addErr
}

type fakeSource struct {
	active        []*v1.Source
	registerErr   error
	runCalls      []string
	runErr        error
	runWebhookErr error
	webhookCalls  atomic.Int32 // touched from loadSources goroutine
}

func (f *fakeSource) Register(context.Context) ([]*v1.Source, error) {
	return f.active, f.registerErr
}

func (f *fakeSource) Run(_ context.Context, src *v1.Source) error {
	f.runCalls = append(f.runCalls, src.ID)
	return f.runErr
}

func (f *fakeSource) RunWebhook(context.Context) error {
	f.webhookCalls.Add(1)
	return f.runWebhookErr
}
func (f *fakeSource) Schedule(*v1.Source) string            { return "*/10 * * * *" }
func (f *fakeSource) Timeout(*v1.Source) time.Duration      { return time.Second }
func (f *fakeSource) SetFilters(map[string][]config.Filter) {}

// newCtrl assembles a Controller via struct literal so tests can omit
// the config controller (the methods under test never read it).
func newCtrl(
	srcs *fakeSourceStore,
	cls *fakeClassificationStore,
	mon *fakeMonitorStore,
	sch *fakeScheduler,
	source *fakeSource,
	g *schema.Guard,
) *Controller {
	if g == nil {
		g = schema.New()
	}
	return &Controller{
		log:             discardLogger(),
		sources:         srcs,
		source:          source,
		classifications: cls,
		monitor:         mon,
		scheduler:       sch,
		guard:           g,
	}
}

func TestSyncSources_NewURLs_GetUpserted(t *testing.T) {
	t.Parallel()
	srcs := &fakeSourceStore{} // empty DB
	ctrl := newCtrl(srcs, nil, nil, nil, nil, nil)

	fresh := []*v1.Source{
		{URL: "https://a", Hash: []byte{1}},
		{URL: "https://b", Hash: []byte{2}},
	}
	if err := ctrl.syncSources(t.Context(), fresh); err != nil {
		t.Fatalf("syncSources: %v", err)
	}
	if len(srcs.upsertCalls) != 1 || len(srcs.upsertCalls[0]) != 2 {
		t.Errorf("upsertCalls = %+v, want one batch of 2", srcs.upsertCalls)
	}
	if srcs.deletedCalls != 0 {
		t.Errorf("nothing to delete, got %d", srcs.deletedCalls)
	}
}

func TestSyncSources_ChangedHash_GetsReUpserted(t *testing.T) {
	t.Parallel()
	srcs := &fakeSourceStore{all: []*v1.Source{
		{URL: "https://a", Hash: []byte{0}},
	}}
	ctrl := newCtrl(srcs, nil, nil, nil, nil, nil)

	fresh := []*v1.Source{{URL: "https://a", Hash: []byte{99}}}
	if err := ctrl.syncSources(t.Context(), fresh); err != nil {
		t.Fatalf("syncSources: %v", err)
	}
	if len(srcs.upsertCalls) != 1 || srcs.upsertCalls[0][0].URL != "https://a" {
		t.Errorf("changed hash should re-upsert: %+v", srcs.upsertCalls)
	}
}

func TestSyncSources_SameHash_SkipsUpsert(t *testing.T) {
	t.Parallel()
	hash := []byte{1, 2, 3}
	srcs := &fakeSourceStore{all: []*v1.Source{{URL: "https://a", Hash: hash}}}
	ctrl := newCtrl(srcs, nil, nil, nil, nil, nil)

	fresh := []*v1.Source{{URL: "https://a", Hash: hash}}
	if err := ctrl.syncSources(t.Context(), fresh); err != nil {
		t.Fatalf("syncSources: %v", err)
	}
	if len(srcs.upsertCalls) != 0 {
		t.Errorf("unchanged hash should skip upsert, got %+v", srcs.upsertCalls)
	}
}

func TestSyncSources_RemovedFromConfig_GetsDeleted(t *testing.T) {
	t.Parallel()
	srcs := &fakeSourceStore{all: []*v1.Source{
		{URL: "https://a", Hash: []byte{1}},
		{URL: "https://stale", Hash: []byte{2}},
	}}
	ctrl := newCtrl(srcs, nil, nil, nil, nil, nil)

	fresh := []*v1.Source{{URL: "https://a", Hash: []byte{1}}}
	if err := ctrl.syncSources(t.Context(), fresh); err != nil {
		t.Fatalf("syncSources: %v", err)
	}
	if diff := cmp.Diff([]string{"https://stale"}, srcs.deletedURLs); diff != "" {
		t.Errorf("deletedURLs mismatch (-want +got):\n%s", diff)
	}
}

func TestSyncSources_StoreError_Bubbles(t *testing.T) {
	t.Parallel()
	srcs := &fakeSourceStore{allErr: errors.New("db down")}
	ctrl := newCtrl(srcs, nil, nil, nil, nil, nil)
	if err := ctrl.syncSources(t.Context(), nil); err == nil {
		t.Error("expected SelectSourceAll error to bubble")
	}
}

func TestSyncTables_DiffCreatesDropsUpdates(t *testing.T) {
	t.Parallel()
	cls := &fakeClassificationStore{
		tables: map[string]struct{}{
			"events": {},
			"stale":  {},
		},
		keys: map[string]map[string]struct{}{
			"events": {"old_field": {}},
		},
	}
	ctrl := newCtrl(nil, cls, nil, nil, nil, nil)

	classifications := map[string][]string{
		"events": {"new_field"},
		"brand":  {"only"},
	}
	if err := ctrl.syncTables(t.Context(), classifications); err != nil {
		t.Fatalf("syncTables: %v", err)
	}

	if diff := cmp.Diff([]string{"stale"}, cls.droppedCalls); diff != "" {
		t.Errorf("dropped mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"brand"}, cls.createdCalls); diff != "" {
		t.Errorf("created mismatch (-want +got):\n%s", diff)
	}
	// "events" exists with field drift → UpdateDataKeyAll called.
	if len(cls.updatedCalls) != 1 || cls.updatedCalls[0].table != "events" {
		t.Errorf("updated calls = %+v, want one for events", cls.updatedCalls)
	}
	got := cls.updatedCalls[0]
	if diff := cmp.Diff([]string{"new_field"}, got.add); diff != "" {
		t.Errorf("add mismatch:\n%s", diff)
	}
	if diff := cmp.Diff([]string{"old_field"}, got.remove); diff != "" {
		t.Errorf("remove mismatch:\n%s", diff)
	}
}

func TestSyncTables_EmptyAttributesIsError(t *testing.T) {
	t.Parallel()
	cls := &fakeClassificationStore{tables: map[string]struct{}{}}
	ctrl := newCtrl(nil, cls, nil, nil, nil, nil)

	classifications := map[string][]string{"events": {}}
	if err := ctrl.syncTables(t.Context(), classifications); err == nil {
		t.Error("expected error for empty attribute list")
	}
}

func TestSyncTables_HoldsWriteLockBlocksReaders(t *testing.T) {
	// While syncTables is running, an RLock acquisition must block; 	// this is the contract pkg/schema relies on to prevent the
	// FK-driven deadlock at the SQL layer.
	t.Parallel()
	g := schema.New()

	createBlocker := make(chan struct{})
	createReleased := make(chan struct{})
	cls := &fakeClassificationStore{
		tables: map[string]struct{}{},
		rLockObs: func() {
			close(createReleased)
			<-createBlocker
		},
	}
	ctrl := newCtrl(nil, cls, nil, nil, nil, g)

	go func() {
		_ = ctrl.syncTables(context.Background(), map[string][]string{"brand": {"x"}})
	}()
	<-createReleased

	var rLocked atomic.Bool
	rlockDone := make(chan struct{})
	go func() {
		g.RLock()
		rLocked.Store(true)
		g.RUnlock()
		close(rlockDone)
	}()

	time.Sleep(50 * time.Millisecond)
	if rLocked.Load() {
		t.Fatal("RLock proceeded while syncTables held write lock")
	}

	close(createBlocker)
	select {
	case <-rlockDone:
	case <-time.After(time.Second):
		t.Fatal("RLock did not unblock after syncTables released the lock")
	}
}

func TestSyncTables_SelectError_Bubbles(t *testing.T) {
	t.Parallel()
	cls := &fakeClassificationStore{tablesErr: errors.New("select bad")}
	ctrl := newCtrl(nil, cls, nil, nil, nil, nil)
	if err := ctrl.syncTables(t.Context(), map[string][]string{}); err == nil {
		t.Error("expected SelectTableAll error to bubble")
	}
}

func TestLoadSources_SchedulesRetrieversAndSpawnsWebhook(t *testing.T) {
	t.Parallel()
	source := &fakeSource{active: []*v1.Source{
		{ID: "rss1", Type: v1.RSSType},
		{ID: "http1", Type: v1.HTTPType},
		{ID: "wh1", Type: v1.WebhookType},
	}}
	sch := &fakeScheduler{}
	ctrl := newCtrl(nil, nil, nil, sch, source, nil)

	if err := ctrl.loadSources(t.Context()); err != nil {
		t.Fatalf("loadSources: %v", err)
	}

	// One Add per retriever (rss + http); webhook is a listener, not scheduled.
	if len(sch.addCalls) != 2 {
		t.Errorf("scheduler.Add called %d times, want 2", len(sch.addCalls))
	}
	labels := []string{}
	for _, c := range sch.addCalls {
		labels = append(labels, c.label)
	}
	wantLabels := []string{"rss:rss1", "http:http1"}
	if diff := cmp.Diff(wantLabels, labels, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
		t.Errorf("schedule labels mismatch (-want +got):\n%s", diff)
	}

	// Webhook listener spawned exactly once. RunWebhook runs in a
	// goroutine; allow a brief window for it to land.
	time.Sleep(50 * time.Millisecond)
	if source.webhookCalls.Load() != 1 {
		t.Errorf("RunWebhook calls = %d, want 1", source.webhookCalls.Load())
	}
}

func TestLoadSources_NoWebhookSources_NoListener(t *testing.T) {
	t.Parallel()
	source := &fakeSource{active: []*v1.Source{
		{ID: "rss1", Type: v1.RSSType},
	}}
	sch := &fakeScheduler{}
	ctrl := newCtrl(nil, nil, nil, sch, source, nil)

	if err := ctrl.loadSources(t.Context()); err != nil {
		t.Fatalf("loadSources: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if source.webhookCalls.Load() != 0 {
		t.Errorf("RunWebhook should not be called without webhook sources, got %d", source.webhookCalls.Load())
	}
}

func TestLoadSources_RegisterError_Bubbles(t *testing.T) {
	t.Parallel()
	source := &fakeSource{registerErr: errors.New("driver init")}
	ctrl := newCtrl(nil, nil, nil, &fakeScheduler{}, source, nil)
	if err := ctrl.loadSources(t.Context()); err == nil {
		t.Error("expected Register error to bubble")
	}
}

func TestUptimeTotal_ForwardsArgs(t *testing.T) {
	t.Parallel()
	mon := &fakeMonitorStore{total: 0.95}
	ctrl := newCtrl(nil, nil, mon, nil, nil, nil)

	got, err := ctrl.UptimeTotal(t.Context(), "S", "E")
	if err != nil {
		t.Fatalf("UptimeTotal: %v", err)
	}
	if got != 0.95 {
		t.Errorf("got %f, want 0.95", got)
	}
	if len(mon.totalArgs) != 1 || mon.totalArgs[0].start != "S" || mon.totalArgs[0].end != "E" {
		t.Errorf("forwarded args = %+v", mon.totalArgs)
	}
}

func TestUptimeSegment_ForwardsFormat(t *testing.T) {
	t.Parallel()
	mon := &fakeMonitorStore{segment: []float64{99.5, 99.9}}
	ctrl := newCtrl(nil, nil, mon, nil, nil, nil)

	got, err := ctrl.UptimeSegment(t.Context(), v1.Month)
	if err != nil {
		t.Fatalf("UptimeSegment: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d buckets, want 2", len(got))
	}
	if len(mon.segmentArgs) != 1 || mon.segmentArgs[0] != v1.Month {
		t.Errorf("forwarded format = %v, want month", mon.segmentArgs)
	}
}
