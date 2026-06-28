package classification

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
	cfgpkg "github.com/notallthere404/futurecast/server/pkg/config"
	"github.com/notallthere404/futurecast/server/pkg/schema"
)

// ClassificationStore is the union of every classification-table
// query the dashboard endpoints reach for plus the bulk insert path
// used by /classifications/insert.
type ClassificationStore interface {
	SelectClassificationCount(context.Context, string, string, string) (int, error)
	SelectFilteredClassification(context.Context, string, string, []string, string, string, float64, int) ([]*v1.LinkedClassification, error)
	SelectFilteredClassMetric(context.Context, string, []string, string, string) ([]*v1.MetricAgg, int, error)
	SelectScoreFrequency(context.Context, string, string, string, string) ([]*v1.LabelWeight, error)
	SelectLabelCounts(context.Context, string, string, string, string, float64) ([]*v1.LabelCount, error)
	SelectFilteredMetric(context.Context, string, string, string, string, []string, float64) ([]*v1.MetricScatter, error)
	SelectLabelDelta(context.Context, string, string, string, string) (*v1.LabelFrequencyAverage, error)
	InsertClassificationBatch(context.Context, string, []*v1.Classification) error
}

// Config is the minimum surface of the config controller used here (Scatter
// reads the label-to-axis-index map). Declared as an interface so tests
// can drop in a fake.
type Config interface {
	Get() *cfgpkg.Config
}

// Query is the parsed search-endpoint query string, normalised into
// the fields the store layer accepts. ParseQuery builds one from raw
// HTTP query-string values.
type Query struct {
	Classification string
	Title          string
	Labels         []string
	Start          string
	End            string
	Cutoff         float64
	Limit          int
}

// Controller serves the dashboard's read endpoints over the
// classification tables plus the bulk-upload endpoint. The classify
// loop itself lives in the inference controller now; sources kick that
// loop after every insert and it self-terminates when there is no more
// unprocessed work.
type Controller struct {
	log             *slog.Logger
	config          Config
	classifications ClassificationStore
	guard           *schema.Guard
}

// New wires the classification controller. The schema.Guard is shared
// with the inference + system controllers so DDL on the classification
// tables can't race ongoing reads.
func New(log *slog.Logger, cfg Config, classifications ClassificationStore, guard *schema.Guard) *Controller {
	return &Controller{
		log:             log.With(slog.String("controller", "classification")),
		config:          cfg,
		classifications: classifications,
		guard:           guard,
	}
}

func (c *Controller) Count(ctx context.Context, req v1.ClassificationCountRequest) (int, error) {
	c.guard.RLock()
	defer c.guard.RUnlock()
	return c.classifications.SelectClassificationCount(ctx, req.Classification, req.Start, req.End)
}

func (c *Controller) Search(ctx context.Context, q Query) ([]*v1.LinkedClassification, error) {
	if q.Limit == 0 {
		q.Limit = 50
	}
	c.guard.RLock()
	defer c.guard.RUnlock()
	return c.classifications.SelectFilteredClassification(ctx, q.Classification, q.Title, q.Labels, q.Start, q.End, q.Cutoff, q.Limit)
}

func (c *Controller) Metrics(ctx context.Context, classification string, labels []string, start, end string) (map[string]*v1.Signal, error) {
	c.guard.RLock()
	defer c.guard.RUnlock()
	metrics, total, err := c.classifications.SelectFilteredClassMetric(ctx, classification, labels, start, end)
	if err != nil {
		return nil, err
	}
	return v1.IntoMappedSignal(metrics, total), nil
}

func (c *Controller) Heatmap(ctx context.Context, req v1.HeatmapRequest) ([]*v1.LabelWeight, error) {
	c.guard.RLock()
	defer c.guard.RUnlock()
	return c.classifications.SelectScoreFrequency(ctx, req.Classification, req.Label, req.Start, req.End)
}

func (c *Controller) Treemap(ctx context.Context, req v1.TreemapRequest) ([]*v1.LabelCount, error) {
	c.guard.RLock()
	defer c.guard.RUnlock()
	return c.classifications.SelectLabelCounts(ctx, req.Classification, req.Attribute, req.Start, req.End, req.Cutoff)
}

func (c *Controller) Plot(ctx context.Context, req v1.PlotRequest) ([]*v1.PlotPoint, error) {
	c.guard.RLock()
	defer c.guard.RUnlock()
	rows, err := c.classifications.SelectFilteredMetric(ctx, "events", "vector", req.Start, req.End, []string{req.Label}, 0)
	if err != nil {
		return nil, err
	}

	response := make([]*v1.PlotPoint, 0, len(rows))
	for _, row := range rows {
		response = append(response, &v1.PlotPoint{
			ArticleID: row.ArticleID,
			Title:     row.Title,
			Link:      row.Link,
			Label:     row.Label,
			X:         row.Timestamp,
			Y:         row.Score,
		})
	}
	return response, nil
}

func (c *Controller) Scatter(ctx context.Context, req v1.ScatterRequest) ([]*v1.ScatterPoint, error) {
	c.guard.RLock()
	defer c.guard.RUnlock()
	rows, err := c.classifications.SelectFilteredMetric(ctx, "events", "vector", req.Start, req.End, req.Labels, req.Cutoff)
	if err != nil {
		return nil, err
	}

	labelMap := c.config.Get().LabelMap()
	response := make([]*v1.ScatterPoint, 0, len(rows))
	for _, row := range rows {
		response = append(response, &v1.ScatterPoint{
			ArticleID: row.ArticleID,
			Title:     row.Title,
			Link:      row.Link,
			Label:     row.Label,
			X:         row.Timestamp,
			Y:         row.Score,
			Z:         labelMap[row.Label],
		})
	}
	return response, nil
}

func (c *Controller) Quadrant(ctx context.Context, req v1.QuadrantRequest) ([]*v1.LabelFrequencyAverage, error) {
	c.guard.RLock()
	defer c.guard.RUnlock()
	a, err := c.classifications.SelectLabelDelta(ctx, "events", req.Label, req.A.Start, req.A.End)
	if err != nil {
		return nil, err
	}

	b, err := c.classifications.SelectLabelDelta(ctx, "events", req.Label, req.B.Start, req.B.End)
	if err != nil {
		return nil, err
	}

	return []*v1.LabelFrequencyAverage{{
		Label:          req.Label,
		Frequency:      b.Frequency / a.Frequency,
		MeanConfidence: b.MeanConfidence - a.MeanConfidence,
	}}, nil
}

func (c *Controller) InsertBatch(ctx context.Context, payload []v1.ClassificationInsertItem) error {
	results := make(map[string][]*v1.Classification)
	for _, class := range payload {
		timestamp, err := time.Parse(time.RFC3339, class.Timestamp)
		if err != nil {
			timestamp = time.Now()
		}

		results[class.Classification] = append(results[class.Classification], &v1.Classification{
			ID:        class.ID,
			ArticleID: class.ArticleID,
			Timestamp: timestamp,
			Data:      class.Data,
		})
	}

	c.guard.RLock()
	defer c.guard.RUnlock()
	for name, classes := range results {
		if err := c.classifications.InsertClassificationBatch(ctx, name, classes); err != nil {
			return err
		}
	}
	return nil
}

// ParseQuery builds a Query from raw HTTP query-string values,
// applying defaults and clamping (limit 1-200, cutoff 0.0-0.99).
func ParseQuery(values map[string][]string) Query {
	q := Query{
		Classification: first(values, "classification"),
		Title:          first(values, "title"),
		Labels:         values["label"],
		Start:          first(values, "start"),
		End:            first(values, "end"),
		Limit:          50,
	}

	if raw := first(values, "limit"); raw != "" {
		if limit, err := strconv.Atoi(raw); err == nil && limit > 0 && limit <= 200 {
			q.Limit = limit
		}
	}

	if raw := first(values, "cutoff"); raw != "" {
		if cutoff, err := strconv.ParseFloat(raw, 64); err == nil && cutoff > 0.0 && cutoff <= 0.99 {
			q.Cutoff = cutoff
		}
	}

	return q
}

func first(values map[string][]string, key string) string {
	items := values[key]
	if len(items) == 0 {
		return ""
	}
	return items[0]
}
