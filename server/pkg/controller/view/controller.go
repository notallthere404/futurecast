package view

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
	configcontroller "github.com/notallthere404/futurecast/server/pkg/controller/config"
)

// Store is the view-storage surface (CRUD on dashboard views).
type Store interface {
	SelectViewAll(context.Context, *string) ([]*v1.View, error)
	SelectViewBySlug(context.Context, string) (*v1.View, error)
	UpsertView(context.Context, *v1.View) error
	DeleteViewBySlug(context.Context, string) error
}

// ClassificationStore is the slice of classification queries the view
// controller dispatches to when resolving a panel's Query at GET time.
type ClassificationStore interface {
	SelectClassificationCount(context.Context, string, string, string) (int, error)
	SelectScoreFrequency(context.Context, string, string, string, string) ([]*v1.LabelWeight, error)
	SelectLabelCounts(context.Context, string, string, string, string, float64) ([]*v1.LabelCount, error)
	SelectFilteredMetric(context.Context, string, string, string, string, []string, float64) ([]*v1.MetricScatter, error)
	SelectLabelDelta(context.Context, string, string, string, string) (*v1.LabelFrequencyAverage, error)
}

// ArticleStore is the slice of article queries the view controller
// reaches for when a panel needs raw article data (spark, list).
type ArticleStore interface {
	SelectArticleRecent(context.Context) ([]*v1.Article, error)
	SelectArticleRate(context.Context, v1.RateFormat) ([]int, error)
}

// SourceStore is the slice of source queries the view controller hits
// for list-source panels.
type SourceStore interface {
	SelectSourceAll(context.Context) ([]*v1.Source, error)
}

// Controller owns dashboard views: CRUD plus the per-panel Query
// resolver that turns declarative Query specs into rendered Viz data.
type Controller struct {
	log             *slog.Logger
	config          *configcontroller.Controller
	views           Store
	classifications ClassificationStore
	articles        ArticleStore
	sources         SourceStore
}

// New wires the view controller. Takes one slice per store the panel
// resolver dispatches to.
func New(log *slog.Logger, cfg *configcontroller.Controller, views Store, classifications ClassificationStore, articles ArticleStore, sources SourceStore) *Controller {
	return &Controller{
		log:             log.With(slog.String("controller", "view")),
		config:          cfg,
		views:           views,
		classifications: classifications,
		articles:        articles,
		sources:         sources,
	}
}

func (c *Controller) List(ctx context.Context, userId *string) ([]*v1.View, error) {
	return c.views.SelectViewAll(ctx, userId)
}

func (c *Controller) Upsert(ctx context.Context, v *v1.View) error {
	return c.views.UpsertView(ctx, v)
}

func (c *Controller) Delete(ctx context.Context, slug string) error {
	return c.views.DeleteViewBySlug(ctx, slug)
}

// Get fetches a view by slug and resolves every panel's Query into a
// Viz envelope. Failures per-panel surface as RenderedPanel.Error so one
// bad panel doesn't break the whole dashboard.
func (c *Controller) Get(ctx context.Context, slug string) (*v1.RenderedView, error) {
	view, err := c.views.SelectViewBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}

	out := &v1.RenderedView{
		ID:    view.ID,
		Slug:  view.Slug,
		Title: view.Title,
	}

	for _, p := range view.Panels {
		rp := v1.RenderedPanel{
			ID: p.ID, X: p.X, Y: p.Y, W: p.W, H: p.H, Title: p.Title,
		}
		viz, err := c.runQuery(ctx, p.Query)
		if err != nil {
			c.log.Error("panel query failed", "view", slug, "panel", p.ID, "kind", p.Query.Kind, "error", err)
			rp.Error = err.Error()
		}
		rp.Viz = viz
		out.Panels = append(out.Panels, rp)
	}

	return out, nil
}

// runQuery dispatches on Query.Kind. Returns a kind-tagged map ready to
// match the client's Viz discriminated union. New VizKinds: add a case.
func (c *Controller) runQuery(ctx context.Context, q v1.Query) (any, error) {
	start, end := resolveRange(q.Range)

	switch q.Kind {
	case v1.VizHeatmap:
		rows, err := c.classifications.SelectScoreFrequency(ctx, q.Classification, q.Label, start, end)
		if err != nil {
			return nil, err
		}
		return viz(q.Kind, rows), nil

	case v1.VizTreemap:
		rows, err := c.classifications.SelectLabelCounts(ctx, q.Classification, q.Attribute, start, end, q.Cutoff)
		if err != nil {
			return nil, err
		}
		return viz(q.Kind, rows), nil

	case v1.VizScatter:
		rows, err := c.classifications.SelectFilteredMetric(ctx, q.Classification, q.Attribute, start, end, q.Labels, q.Cutoff)
		if err != nil {
			return nil, err
		}
		out := make([]*v1.PlotPoint, 0, len(rows))
		for _, r := range rows {
			out = append(out, &v1.PlotPoint{
				ArticleID: r.ArticleID, Title: r.Title, Link: r.Link, Label: r.Label,
				X: r.Timestamp, Y: r.Score,
			})
		}
		return viz(q.Kind, out), nil

	case v1.VizScatter3D:
		rows, err := c.classifications.SelectFilteredMetric(ctx, q.Classification, q.Attribute, start, end, q.Labels, q.Cutoff)
		if err != nil {
			return nil, err
		}
		labelMap := c.config.Get().LabelMap()
		out := make([]*v1.ScatterPoint, 0, len(rows))
		for _, r := range rows {
			out = append(out, &v1.ScatterPoint{
				ArticleID: r.ArticleID, Title: r.Title, Link: r.Link, Label: r.Label,
				X: r.Timestamp, Y: r.Score, Z: labelMap[r.Label],
			})
		}
		return viz(q.Kind, out), nil

	case v1.VizSpark:
		format := v1.Day
		if q.Range != nil && q.Range.Unit == "month" {
			format = v1.Month
		}
		rows, err := c.articles.SelectArticleRate(ctx, format)
		if err != nil {
			return nil, err
		}
		points := make([]map[string]any, 0, len(rows))
		for i, v := range rows {
			points = append(points, map[string]any{"index": i, "value": v})
		}
		variant := q.Variant
		if variant == "" {
			variant = "line"
		}
		return map[string]any{
			"kind":    q.Kind,
			"variant": variant,
			"data":    points,
			"format":  fmtOr(q.Format, "n"),
		}, nil

	case v1.VizList:
		switch q.Variant {
		case "source":
			rows, err := c.sources.SelectSourceAll(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"kind": q.Kind, "variant": "source", "data": rows}, nil
		case "article", "":
			rows, err := c.articles.SelectArticleRecent(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"kind": q.Kind, "variant": "article", "data": rows}, nil
		default:
			return nil, fmt.Errorf("unknown list variant %q", q.Variant)
		}

	case v1.VizStats:
		// Target picks which scalar to fetch. Extend as needed.
		switch q.Target {
		case "classification_count", "":
			n, err := c.classifications.SelectClassificationCount(ctx, q.Classification, start, end)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"kind": q.Kind, "data": n, "format": fmtOr(q.Format, "n"),
			}, nil
		default:
			return nil, fmt.Errorf("unknown stats target %q", q.Target)
		}

	case v1.VizQuadrant:
		if q.RangeB == nil {
			return nil, errors.New("quadrant query requires range_b")
		}
		aStart, aEnd := resolveRange(q.Range)
		bStart, bEnd := resolveRange(q.RangeB)
		a, err := c.classifications.SelectLabelDelta(ctx, q.Classification, q.Label, aStart, aEnd)
		if err != nil {
			return nil, err
		}
		b, err := c.classifications.SelectLabelDelta(ctx, q.Classification, q.Label, bStart, bEnd)
		if err != nil {
			return nil, err
		}
		data := []*v1.LabelFrequencyAverage{{
			Label:          q.Label,
			Frequency:      safeDiv(b.Frequency, a.Frequency),
			MeanConfidence: b.MeanConfidence - a.MeanConfidence,
		}}
		return viz(q.Kind, data), nil

	case v1.VizStatus, v1.VizTree:
		// TODO: implement once executor needs land.
		return nil, fmt.Errorf("viz kind %q not yet implemented", q.Kind)

	default:
		return nil, fmt.Errorf("unknown viz kind %q", q.Kind)
	}
}

// resolveRange absolute Start/End wins if set; else relative (Unit +
// Count) computed against now. Returns RFC3339 strings ready for queries.
func resolveRange(r *v1.Range) (string, string) {
	if r == nil {
		return "", ""
	}
	if r.Start != "" || r.End != "" {
		return r.Start, r.End
	}
	if r.Count <= 0 || r.Unit == "" {
		return "", ""
	}
	end := time.Now().UTC()
	start := end.Add(-unitDuration(r.Unit) * time.Duration(r.Count))
	return start.Format(time.RFC3339), end.Format(time.RFC3339)
}

func unitDuration(unit string) time.Duration {
	switch unit {
	case "day":
		return 24 * time.Hour
	case "week":
		return 7 * 24 * time.Hour
	case "month":
		return 30 * 24 * time.Hour
	case "year":
		return 365 * 24 * time.Hour
	}
	return 0
}

func viz(kind v1.VizKind, data any) map[string]any {
	return map[string]any{"kind": kind, "data": data}
}

func fmtOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}
