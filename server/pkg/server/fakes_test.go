package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/notallthere404/futurecast/server/pkg/config"
	"github.com/notallthere404/futurecast/server/pkg/logger"

	v1 "github.com/notallthere404/futurecast/server/api/v1"

	classificationcontroller "github.com/notallthere404/futurecast/server/pkg/controller/classification"
)

// Lightweight fakes that implement the per-dep interfaces declared in
// server.go. Tests set the *Out / *Err fields to script behaviour and
// inspect *Calls / Last* to verify the handler forwarded inputs
// correctly. No reflection, no mocking framework; each fake is a
// hand-rolled stub that does exactly what its handler needs.

type fakeConfig struct {
	clientCfg config.ClientConfig
	err       error
}

func (f *fakeConfig) ClientConfig() (config.ClientConfig, error) { return f.clientCfg, f.err }

type fakeSystem struct {
	updateErr      error
	restartErr     error
	uptimeTotal    float64
	uptimeTotalErr error
	uptimeSeg      []float64
	uptimeSegErr   error

	updateCalls  []string
	restartCalls int
	totalCalls   []struct{ start, end string }
	segCalls     []v1.RateFormat
}

func (f *fakeSystem) UpdateConfig(raw string) error {
	f.updateCalls = append(f.updateCalls, raw)
	return f.updateErr
}
func (f *fakeSystem) Restart() error { f.restartCalls++; return f.restartErr }
func (f *fakeSystem) UptimeTotal(_ context.Context, start, end string) (float64, error) {
	f.totalCalls = append(f.totalCalls, struct{ start, end string }{start, end})
	return f.uptimeTotal, f.uptimeTotalErr
}

func (f *fakeSystem) UptimeSegment(_ context.Context, format v1.RateFormat) ([]float64, error) {
	f.segCalls = append(f.segCalls, format)
	return f.uptimeSeg, f.uptimeSegErr
}

type fakeSources struct {
	list      []*v1.Source
	listErr   error
	upsertErr error
	batchErr  error
	recent    []*v1.Article
	recentErr error
	rate      []int
	rateErr   error
	runRSSErr error
	webhook   http.Handler

	upsertCalls []*v1.Source
	batchCalls  [][]*v1.Source
	rateCalls   []v1.RateFormat
	runRSSCalls int
}

func (f *fakeSources) List(context.Context) ([]*v1.Source, error) { return f.list, f.listErr }
func (f *fakeSources) Upsert(_ context.Context, src *v1.Source) error {
	f.upsertCalls = append(f.upsertCalls, src)
	return f.upsertErr
}

func (f *fakeSources) UpsertBatch(_ context.Context, srcs []*v1.Source) error {
	f.batchCalls = append(f.batchCalls, srcs)
	return f.batchErr
}

func (f *fakeSources) Recent(context.Context) ([]*v1.Article, error) {
	return f.recent, f.recentErr
}

func (f *fakeSources) Rate(_ context.Context, format v1.RateFormat) ([]int, error) {
	f.rateCalls = append(f.rateCalls, format)
	return f.rate, f.rateErr
}
func (f *fakeSources) RunRSS(context.Context) error { f.runRSSCalls++; return f.runRSSErr }
func (f *fakeSources) WebhookHandler() http.Handler { return f.webhook }

type fakeViews struct {
	list      []*v1.View
	listErr   error
	get       *v1.RenderedView
	getErr    error
	upsertErr error
	deleteErr error
}

func (f *fakeViews) List(_ context.Context, _ *string) ([]*v1.View, error) {
	return f.list, f.listErr
}

func (f *fakeViews) Get(_ context.Context, _ string) (*v1.RenderedView, error) {
	return f.get, f.getErr
}
func (f *fakeViews) Upsert(_ context.Context, _ *v1.View) error { return f.upsertErr }
func (f *fakeViews) Delete(_ context.Context, _ string) error   { return f.deleteErr }

type fakeClassifications struct {
	search      []*v1.LinkedClassification
	searchErr   error
	count       int
	countErr    error
	insertErr   error
	metrics     map[string]*v1.Signal
	metricsErr  error
	heatmap     []*v1.LabelWeight
	heatmapErr  error
	treemap     []*v1.LabelCount
	treemapErr  error
	plot        []*v1.PlotPoint
	plotErr     error
	scatter     []*v1.ScatterPoint
	scatterErr  error
	quadrant    []*v1.LabelFrequencyAverage
	quadrantErr error

	lastQuery   classificationcontroller.Query
	insertCalls [][]v1.ClassificationInsertItem
	lastMetrics struct {
		classification, start, end string
		labels                     []string
	}
}

func (f *fakeClassifications) Search(_ context.Context, q classificationcontroller.Query) ([]*v1.LinkedClassification, error) {
	f.lastQuery = q
	return f.search, f.searchErr
}

func (f *fakeClassifications) Count(_ context.Context, _ v1.ClassificationCountRequest) (int, error) {
	return f.count, f.countErr
}

func (f *fakeClassifications) InsertBatch(_ context.Context, p []v1.ClassificationInsertItem) error {
	f.insertCalls = append(f.insertCalls, p)
	return f.insertErr
}

func (f *fakeClassifications) Metrics(_ context.Context, classification string, labels []string, start, end string) (map[string]*v1.Signal, error) {
	f.lastMetrics.classification = classification
	f.lastMetrics.labels = labels
	f.lastMetrics.start = start
	f.lastMetrics.end = end
	return f.metrics, f.metricsErr
}

func (f *fakeClassifications) Heatmap(_ context.Context, _ v1.HeatmapRequest) ([]*v1.LabelWeight, error) {
	return f.heatmap, f.heatmapErr
}

func (f *fakeClassifications) Treemap(_ context.Context, _ v1.TreemapRequest) ([]*v1.LabelCount, error) {
	return f.treemap, f.treemapErr
}

func (f *fakeClassifications) Plot(_ context.Context, _ v1.PlotRequest) ([]*v1.PlotPoint, error) {
	return f.plot, f.plotErr
}

func (f *fakeClassifications) Scatter(_ context.Context, _ v1.ScatterRequest) ([]*v1.ScatterPoint, error) {
	return f.scatter, f.scatterErr
}

func (f *fakeClassifications) Quadrant(_ context.Context, _ v1.QuadrantRequest) ([]*v1.LabelFrequencyAverage, error) {
	return f.quadrant, f.quadrantErr
}

type fakeInference struct {
	kicks int
}

func (f *fakeInference) Kick() { f.kicks++ }

type fakeScheduler struct {
	runCalls    int
	stopCalls   int
	removeCalls []string
}

func (f *fakeScheduler) Run()                { f.runCalls++ }
func (f *fakeScheduler) Stop()               { f.stopCalls++ }
func (f *fakeScheduler) Remove(label string) { f.removeCalls = append(f.removeCalls, label) }

// testServer assembles a *Server with the supplied (often partially-
// populated) fakes; nil fakes are replaced with zero-value defaults so
// individual tests only construct what they need.
func testServer(
	cfg *fakeConfig,
	sys *fakeSystem,
	src *fakeSources,
	views *fakeViews,
	cls *fakeClassifications,
	inf *fakeInference,
	sch *fakeScheduler,
) *Server {
	if cfg == nil {
		cfg = &fakeConfig{}
	}
	if sys == nil {
		sys = &fakeSystem{}
	}
	if src == nil {
		src = &fakeSources{}
	}
	if views == nil {
		views = &fakeViews{}
	}
	if cls == nil {
		cls = &fakeClassifications{}
	}
	if inf == nil {
		inf = &fakeInference{}
	}
	if sch == nil {
		sch = &fakeScheduler{}
	}
	log := slog.New(slog.DiscardHandler)
	return New(log, logger.NewBroadcaster(), "127.0.0.1:0", cfg, sys, src, views, cls, inf, sch)
}
