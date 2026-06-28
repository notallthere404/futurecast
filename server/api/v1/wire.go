package v1

import "time"

// ErrorDetail is the inner shape of an error response: stable code for
// programmatic handling, human-readable message, optional field that
// caused validation failures.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// ErrorResponse is the wrapper every failed HTTP route returns so
// dashboards parse one shape regardless of the failing endpoint.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ConfigUpdateRequest carries a raw YAML config blob from the dashboard
// editor; the server writes it to disk and triggers a reload.
type ConfigUpdateRequest struct {
	Config string `json:"config"`
}

// ClassificationCountRequest is the body of a count query over a
// classification's rows within an inclusive time range.
type ClassificationCountRequest struct {
	Classification string `json:"classification"`
	Start          string `json:"start"`
	End            string `json:"end"`
}

// HeatmapRequest is the body of a daily-frequency query for a single
// label within a classification over a time range.
type HeatmapRequest struct {
	Classification string `json:"classification"`
	Label          string `json:"label"`
	Start          string `json:"start"`
	End            string `json:"end"`
}

// TreemapRequest is the body of a label-count query, optionally scoped
// to one attribute and filtered by score cutoff.
type TreemapRequest struct {
	Classification string  `json:"classification"`
	Attribute      string  `json:"attribute"`
	Start          string  `json:"start"`
	End            string  `json:"end"`
	Cutoff         float64 `json:"cutoff"`
}

// PlotRequest is the body of a label-frequency over time query.
type PlotRequest struct {
	Classification string `json:"classification"`
	Label          string `json:"label"`
	Start          string `json:"start"`
	End            string `json:"end"`
}

// ScatterRequest is the body of a per-article score query, returning
// one point per (article, label) pair above the cutoff.
type ScatterRequest struct {
	Classification string   `json:"classification"`
	Attribute      string   `json:"attribute"`
	Cutoff         float64  `json:"cutoff"`
	Labels         []string `json:"labels"`
	Start          string   `json:"start"`
	End            string   `json:"end"`
}

// RangeRequest is a simple time window used standalone or embedded in
// requests that need two windows (e.g. QuadrantRequest).
type RangeRequest struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// QuadrantRequest compares one label across two time windows so the
// dashboard can render frequency/confidence drift between periods.
type QuadrantRequest struct {
	Classification string       `json:"classification"`
	Label          string       `json:"label"`
	A              RangeRequest `json:"a"`
	B              RangeRequest `json:"b"`
}

// RateRequest is the body of an article-rate query; Format picks the
// bucket granularity (see RateFormat).
type RateRequest struct {
	Format RateFormat `json:"format"`
}

// UptimeRequest is the body of an uptime-percentage query over a range.
type UptimeRequest struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// PlotPoint is one (X=time, Y=score) point with the originating article
// for click-through, used by the line/area plot viz.
type PlotPoint struct {
	ArticleID string    `json:"article_id"`
	Title     string    `json:"title"`
	Link      string    `json:"link"`
	Label     string    `json:"label"`
	X         time.Time `json:"x"`
	Y         float64   `json:"y"`
}

// ScatterPoint is PlotPoint with a third axis Z — used by the 3D
// scatter viz where Z carries a derived signal (e.g. label-axis index).
type ScatterPoint struct {
	ArticleID string    `json:"article_id"`
	Title     string    `json:"title"`
	Link      string    `json:"link"`
	Label     string    `json:"label"`
	X         time.Time `json:"x"`
	Y         float64   `json:"y"`
	Z         float64   `json:"z"`
}
