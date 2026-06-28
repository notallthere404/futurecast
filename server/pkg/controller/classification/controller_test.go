package classification

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/notallthere404/futurecast/server/pkg/schema"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
	cfgpkg "github.com/notallthere404/futurecast/server/pkg/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

type fakeClassificationStore struct {
	count          int
	countErr       error
	search         []*v1.LinkedClassification
	searchErr      error
	classMetric    []*v1.MetricAgg
	classMetricTot int
	classMetricErr error
	scoreFreq      []*v1.LabelWeight
	scoreFreqErr   error
	labelCounts    []*v1.LabelCount
	labelCountsErr error
	scatter        []*v1.MetricScatter
	scatterErr     error
	labelDelta     *v1.LabelFrequencyAverage
	labelDeltaErr  error
	insertErr      error

	insertCalls []struct {
		name    string
		classes []*v1.Classification
	}
}

func (f *fakeClassificationStore) SelectClassificationCount(_ context.Context, classification, start, end string) (int, error) {
	_ = classification
	_ = start
	_ = end
	return f.count, f.countErr
}

func (f *fakeClassificationStore) SelectFilteredClassification(_ context.Context, _, _ string, _ []string, _, _ string, _ float64, _ int) ([]*v1.LinkedClassification, error) {
	return f.search, f.searchErr
}

func (f *fakeClassificationStore) SelectFilteredClassMetric(_ context.Context, _ string, _ []string, _, _ string) ([]*v1.MetricAgg, int, error) {
	return f.classMetric, f.classMetricTot, f.classMetricErr
}

func (f *fakeClassificationStore) SelectScoreFrequency(_ context.Context, _, _, _, _ string) ([]*v1.LabelWeight, error) {
	return f.scoreFreq, f.scoreFreqErr
}

func (f *fakeClassificationStore) SelectLabelCounts(_ context.Context, _, _, _, _ string, _ float64) ([]*v1.LabelCount, error) {
	return f.labelCounts, f.labelCountsErr
}

func (f *fakeClassificationStore) SelectFilteredMetric(_ context.Context, _, _, _, _ string, _ []string, _ float64) ([]*v1.MetricScatter, error) {
	return f.scatter, f.scatterErr
}

func (f *fakeClassificationStore) SelectLabelDelta(_ context.Context, _, _, _, _ string) (*v1.LabelFrequencyAverage, error) {
	return f.labelDelta, f.labelDeltaErr
}

func (f *fakeClassificationStore) InsertClassificationBatch(_ context.Context, name string, classes []*v1.Classification) error {
	f.insertCalls = append(f.insertCalls, struct {
		name    string
		classes []*v1.Classification
	}{name, classes})
	return f.insertErr
}

type fakeConfig struct{ cfg *cfgpkg.Config }

func (f *fakeConfig) Get() *cfgpkg.Config { return f.cfg }

func newCtrl(cls *fakeClassificationStore, cfg *fakeConfig, g *schema.Guard) *Controller {
	if g == nil {
		g = schema.New()
	}
	if cfg == nil {
		cfg = &fakeConfig{cfg: &cfgpkg.Config{}}
	}
	return New(discardLogger(), cfg, cls, g)
}

func TestCount_Forwards(t *testing.T) {
	t.Parallel()
	cls := &fakeClassificationStore{count: 7}
	ctrl := newCtrl(cls, nil, nil)

	got, err := ctrl.Count(t.Context(), v1.ClassificationCountRequest{Classification: "events"})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if got != 7 {
		t.Errorf("got %d, want 7", got)
	}
}

func TestSearch_DefaultLimit50(t *testing.T) {
	// The Query DSL allows omitting Limit; the controller patches it to
	// 50 so the downstream SQL never receives an unbounded query.
	t.Parallel()
	cls := &fakeClassificationStore{}
	ctrl := newCtrl(cls, nil, nil)

	if _, err := ctrl.Search(t.Context(), Query{Classification: "events" /* Limit zero */}); err != nil {
		t.Fatalf("Search: %v", err)
	}
}

func TestMetrics_WrapsMapped(t *testing.T) {
	t.Parallel()
	cls := &fakeClassificationStore{classMetric: []*v1.MetricAgg{{Label: "a", Count: 5, Mean: 0.5}}, classMetricTot: 5}
	ctrl := newCtrl(cls, nil, nil)

	got, err := ctrl.Metrics(t.Context(), "events", []string{"a"}, "", "")
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	if _, ok := got["a"]; !ok {
		t.Errorf("expected label %q in result, got %+v", "a", got)
	}
}

func TestHeatmap_Treemap_Plot_Quadrant_Forward(t *testing.T) {
	t.Parallel()
	cls := &fakeClassificationStore{
		scoreFreq:   []*v1.LabelWeight{{}},
		labelCounts: []*v1.LabelCount{{}},
		scatter:     []*v1.MetricScatter{{ArticleID: "a", Score: 0.7, Label: "x"}},
		labelDelta:  &v1.LabelFrequencyAverage{Frequency: 2, MeanConfidence: 0.3},
	}
	ctrl := newCtrl(cls, nil, nil)

	if _, err := ctrl.Heatmap(t.Context(), v1.HeatmapRequest{}); err != nil {
		t.Errorf("Heatmap: %v", err)
	}
	if _, err := ctrl.Treemap(t.Context(), v1.TreemapRequest{}); err != nil {
		t.Errorf("Treemap: %v", err)
	}
	if _, err := ctrl.Plot(t.Context(), v1.PlotRequest{Label: "x"}); err != nil {
		t.Errorf("Plot: %v", err)
	}
	got, err := ctrl.Quadrant(t.Context(), v1.QuadrantRequest{Label: "x"})
	if err != nil {
		t.Errorf("Quadrant: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("Quadrant returned %d, want 1", len(got))
	}
}

func TestScatter_AttachesLabelMapZ(t *testing.T) {
	// The Scatter handler enriches each row with a Z coordinate from
	// the config's LabelMap (label-to-axis-index). We construct a real
	// minimal Config that produces a known label index.
	t.Parallel()
	cfg := &cfgpkg.Config{}
	cfg.Inference.Classifications = map[string][]cfgpkg.InferenceAttribute{
		"events": {{Name: "vector", Labels: []cfgpkg.InferenceLabel{{Name: "x"}, {Name: "y"}}}},
	}

	cls := &fakeClassificationStore{
		scatter: []*v1.MetricScatter{
			{ArticleID: "a1", Label: "x", Score: 0.5, Timestamp: time.Now()},
			{ArticleID: "a2", Label: "y", Score: 0.7, Timestamp: time.Now()},
		},
	}
	ctrl := newCtrl(cls, &fakeConfig{cfg: cfg}, nil)

	got, err := ctrl.Scatter(t.Context(), v1.ScatterRequest{Classification: "events"})
	if err != nil {
		t.Fatalf("Scatter: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d points, want 2", len(got))
	}
	if got[0].Z == got[1].Z {
		t.Errorf("expected distinct Z per label, got %v", got)
	}
}

func TestInsertBatch_GroupsByClassification(t *testing.T) {
	t.Parallel()
	cls := &fakeClassificationStore{}
	ctrl := newCtrl(cls, nil, nil)

	payload := []v1.ClassificationInsertItem{
		{Classification: "events", ID: "1", ArticleID: "a1", Timestamp: "2025-01-01T00:00:00Z"},
		{Classification: "events", ID: "2", ArticleID: "a2", Timestamp: "2025-01-01T00:00:00Z"},
		{Classification: "brand", ID: "3", ArticleID: "a3", Timestamp: "2025-01-01T00:00:00Z"},
	}
	if err := ctrl.InsertBatch(t.Context(), payload); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if len(cls.insertCalls) != 2 {
		t.Errorf("expected one InsertClassificationBatch per classification (2), got %d", len(cls.insertCalls))
	}
	for _, c := range cls.insertCalls {
		if c.name == "events" && len(c.classes) != 2 {
			t.Errorf("events batch should hold 2 items, got %d", len(c.classes))
		}
		if c.name == "brand" && len(c.classes) != 1 {
			t.Errorf("brand batch should hold 1 item, got %d", len(c.classes))
		}
	}
}

func TestInsertBatch_BadTimestampFallsBackToNow(t *testing.T) {
	// Bad RFC3339 timestamps default to time.Now rather than erroring;
	// upstream uploaders that send sloppy timestamps must still land.
	t.Parallel()
	cls := &fakeClassificationStore{}
	ctrl := newCtrl(cls, nil, nil)

	before := time.Now()
	err := ctrl.InsertBatch(t.Context(), []v1.ClassificationInsertItem{
		{Classification: "events", ID: "1", ArticleID: "a1", Timestamp: "not-a-time"},
	})
	after := time.Now()
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if len(cls.insertCalls) != 1 {
		t.Fatalf("expected one batch, got %d", len(cls.insertCalls))
	}
	ts := cls.insertCalls[0].classes[0].Timestamp
	if ts.Before(before) || ts.After(after) {
		t.Errorf("bad timestamp fallback %v not in [%v, %v]", ts, before, after)
	}
}

func TestParseQuery(t *testing.T) {
	t.Parallel()
	values := map[string][]string{
		"classification": {"events"},
		"title":          {"cve"},
		"label":          {"a", "b"},
		"start":          {"S"},
		"end":            {"E"},
		"limit":          {"100"},
		"cutoff":         {"0.4"},
	}
	q := ParseQuery(values)
	if q.Classification != "events" || q.Title != "cve" || q.Start != "S" || q.End != "E" {
		t.Errorf("base fields = %+v", q)
	}
	if len(q.Labels) != 2 {
		t.Errorf("labels = %v", q.Labels)
	}
	if q.Limit != 100 || q.Cutoff != 0.4 {
		t.Errorf("limit/cutoff = %d/%f", q.Limit, q.Cutoff)
	}
}

func TestParseQuery_GuardsAgainstOutOfRange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		values map[string][]string
		want   Query
	}{
		{
			name:   "default limit",
			values: map[string][]string{},
			want:   Query{Limit: 50},
		},
		{
			name:   "limit too high ignored",
			values: map[string][]string{"limit": {"500"}},
			want:   Query{Limit: 50},
		},
		{
			name:   "limit non-numeric ignored",
			values: map[string][]string{"limit": {"abc"}},
			want:   Query{Limit: 50},
		},
		{
			name:   "cutoff above 0.99 ignored",
			values: map[string][]string{"cutoff": {"1.5"}},
			want:   Query{Limit: 50, Cutoff: 0},
		},
		{
			name:   "cutoff zero ignored",
			values: map[string][]string{"cutoff": {"0"}},
			want:   Query{Limit: 50, Cutoff: 0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseQuery(tc.values)
			if got.Limit != tc.want.Limit {
				t.Errorf("Limit = %d, want %d", got.Limit, tc.want.Limit)
			}
			if got.Cutoff != tc.want.Cutoff {
				t.Errorf("Cutoff = %f, want %f", got.Cutoff, tc.want.Cutoff)
			}
		})
	}
}
