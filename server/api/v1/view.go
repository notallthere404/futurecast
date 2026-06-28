package v1

import (
	"encoding/json"
	"time"
)

// View is a stored dashboard: slug-addressed page of panels. Each panel
// carries position + a declarative Query. The server resolves queries
// at GET time into RenderedView for the client.
type View struct {
	ID          string          `json:"id" db:"id"`
	Slug        string          `json:"slug" db:"slug"`
	Title       string          `json:"title" db:"title"`
	Description string          `json:"description" db:"description"`
	UserID      *string         `json:"user_id,omitempty" db:"user_id"`
	Panels      []PanelSpec     `json:"panels" db:"-"`
	PanelsRaw   json.RawMessage `json:"-" db:"panels"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt   *time.Time      `json:"updated_at" db:"updated_at"`
}

// HydratePanels decodes PanelsRaw (set by the DB layer) into Panels.
// Repo scans the jsonb column into PanelsRaw; callers invoke this once.
func (v *View) HydratePanels() error {
	if v.Panels != nil || len(v.PanelsRaw) == 0 {
		return nil
	}
	return json.Unmarshal(v.PanelsRaw, &v.Panels)
}

// VizKind discriminates both the panel's display type and the shape
// of its Query. One Query per VizKind; the executor dispatches on this.
type VizKind string

const (
	VizStats     VizKind = "stats"
	VizStatus    VizKind = "status"
	VizList      VizKind = "list"
	VizSpark     VizKind = "spark"
	VizTree      VizKind = "tree"
	VizHeatmap   VizKind = "heatmap"
	VizTreemap   VizKind = "treemap"
	VizQuadrant  VizKind = "quadrant"
	VizScatter   VizKind = "scatter"
	VizScatter3D VizKind = "scatter3d"
)

// PanelSpec is the stored shape. Position + query, no data. Lives inside the
// view's panels jsonb column.
type PanelSpec struct {
	ID    string `json:"id"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
	W     int    `json:"w"`
	H     int    `json:"h"`
	Title string `json:"title,omitempty"`
	Query Query  `json:"query"`
}

// Query is the declarative spec the server resolves into data. Fields
// are populated per Kind; unused fields stay zero. Add fields as new
// VizKinds need them; the JSON wire format absorbs unknown fields
// gracefully.
type Query struct {
	Kind           VizKind  `json:"kind"`
	Classification string   `json:"classification,omitempty"`
	Label          string   `json:"label,omitempty"`
	Labels         []string `json:"labels,omitempty"`
	Attribute      string   `json:"attribute,omitempty"`
	Cutoff         float64  `json:"cutoff,omitempty"`
	Range          *Range   `json:"range,omitempty"`
	RangeB         *Range   `json:"range_b,omitempty"` // quadrant: B-period
	Limit          int      `json:"limit,omitempty"`
	Format         string   `json:"format,omitempty"`  // stats: n|k|f|%
	Target         string   `json:"target,omitempty"`  // status: probe target
	Variant        string   `json:"variant,omitempty"` // spark: line|bar ; list: source|article
}

// Range expresses either a relative window (Unit + Count, e.g. "last
// 7 days") or an absolute one (Start + End). The resolver picks
// absolute first when Start is set.
type Range struct {
	Unit  string `json:"unit,omitempty"` // day|week|month|year
	Count int    `json:"count,omitempty"`
	Start string `json:"start,omitempty"` // RFC3339 override
	End   string `json:"end,omitempty"`
}

// RenderedView is the GET /views/{slug} response. Panels carry resolved data.
type RenderedView struct {
	ID     string          `json:"id"`
	Slug   string          `json:"slug"`
	Title  string          `json:"title"`
	Panels []RenderedPanel `json:"panels"`
}

// RenderedPanel is position + the Viz envelope (kind + data) ready
// for the client's existing Viz union.
type RenderedPanel struct {
	ID    string `json:"id"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
	W     int    `json:"w"`
	H     int    `json:"h"`
	Title string `json:"title,omitempty"`
	Viz   any    `json:"viz"`
	Error string `json:"error,omitempty"` // populated if executor failed; data omitted
}
