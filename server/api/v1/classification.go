package v1

import "time"

// MappedClassArray groups classifications by classification name (the
// dynamic top-level key in the config's `inference:` block; one entry
// per classification table). The inference worker accumulates results
// in this shape between persist calls.
//
// Example payload:
//
//	"news": [
//	    {
//	        "id": "0000-0000-0011",
//	        "article_id": "0000-0000-0001",
//	        "timestamp": "2026-04-09T14:22:00Z",
//	        "data": {
//	            "event_type": [{"label": "natural disaster", "score": 0.56}],
//	            "sentiment":  [{"label": "anger", "score": 0.72}]
//	        }
//	    }
//	]
type MappedClassArray map[string][]*Classification

// Classification is one processed article: the article reference plus
// the attribute-keyed scores the classifier returned. Stored one row
// per Classification in the per-classification table.
type Classification struct {
	ID        string    `json:"id" db:"id"`
	ArticleID string    `json:"article_id" db:"article_id"`
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
	// Data keys are attribute names; values are the ranked label scores
	// for that attribute.
	Data map[string][]*LabelScore `json:"data" db:"data"`
}

// LabelScore is the smallest unit of classification output: a label
// name and the model's confidence (0.0-1.0).
type LabelScore struct {
	Label string  `json:"label" db:"label"`
	Score float64 `json:"score" db:"score"`
}

// LinkedClassification is a Classification joined with its source
// article's display fields (title, link). Used by search results where
// the dashboard needs to render the article alongside the scores.
type LinkedClassification struct {
	Classification
	Title string `json:"title" db:"title"`
	Link  string `json:"link" db:"link"`
}

// LabelCount is the per-label aggregate used by the treemap viz.
type LabelCount struct {
	Label string `json:"label" db:"label"`
	Count int    `json:"count" db:"count"`
}

// LabelWeight is a date-bucketed weight used by the heatmap viz.
type LabelWeight struct {
	Date  time.Time `db:"day" json:"date"`
	Value float64   `db:"value" json:"value"`
}

// LabelFrequencyAverage pairs a label's frequency with its mean
// confidence over a time range — the building block for the quadrant
// viz that compares two periods.
type LabelFrequencyAverage struct {
	Label          string  `json:"label" db:"label"`
	Frequency      float64 `json:"frequency" db:"frequency"`
	MeanConfidence float64 `json:"mean_confidence" db:"mean_confidence"`
}

// MetricScatter is one (article, label) scored point joined with
// display fields; the scatter viz renders one of these per row.
type MetricScatter struct {
	ArticleID string    `json:"article_id" db:"article_id"`
	Title     string    `json:"title" db:"title"`
	Link      string    `json:"link" db:"link"`
	Label     string    `json:"label" db:"label"`
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
	Score     float64   `json:"score" db:"score"`
}

// Metric is one flattened (article, attribute, label, score) row,
// stored in the per-classification metrics table. One Classification
// fans out to len(Data) * len(scores) Metrics; IntoMetrics handles
// the flattening.
type Metric struct {
	ArticleID string    `json:"article_id" db:"article_id"`
	Category  string    `json:"category" db:"category"`
	Label     string    `json:"label" db:"label"`
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
	Score     float64   `json:"score" db:"score"`
}

// ClassificationInsertItem is the wire shape an inbound classify
// response uses before the controller parses it into a Classification.
// Timestamp stays a string here because the inference service returns
// RFC3339 text, not a parsed time.
type ClassificationInsertItem struct {
	Classification string                   `json:"classification"`
	ID             string                   `json:"id"`
	ArticleID      string                   `json:"article_id"`
	Timestamp      string                   `json:"timestamp"`
	Data           map[string][]*LabelScore `json:"data"`
}

// IntoMetrics flattens a Classification into one Metric per
// (attribute, label) score. The flattened rows feed the per-classification
// metrics table that the dashboard's aggregation queries read.
func (dyn *Classification) IntoMetrics() []*Metric {
	var metrics []*Metric
	for category, labelscores := range dyn.Data {
		for _, ls := range labelscores {
			metrics = append(metrics, &Metric{
				ArticleID: dyn.ArticleID,
				Category:  category,
				Label:     ls.Label,
				Timestamp: dyn.Timestamp,
				Score:     ls.Score,
			})
		}
	}
	return metrics
}
