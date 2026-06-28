package classification

import (
	"context"
	"fmt"
	"strings"

	"github.com/notallthere404/futurecast/server/api/v1"

	"github.com/jackc/pgx/v5"
)

// InsertClassificationBatch writes classifications + their flattened metrics
// using COPY for both tables in a single transaction. COPY is significantly
// faster than INSERT loops (one round trip, no per-row parsing).
func (s *Store) InsertClassificationBatch(ctx context.Context, name string, data []*v1.Classification) error {
	if len(data) == 0 {
		return nil
	}

	n := strings.ToLower(name)
	classificationTable := pgx.Identifier{n}
	metricsTable := pgx.Identifier{n + "_metrics"}

	// Build classification rows.
	classificationRows := make([][]any, 0, len(data))
	for _, d := range data {
		classificationRows = append(classificationRows, []any{
			d.ID,
			d.ArticleID,
			d.Timestamp,
			d.Data,
		})
	}

	// Build flattened metric rows.
	var metricRows [][]any
	for _, d := range data {
		for _, m := range d.IntoMetrics() {
			metricRows = append(metricRows, []any{
				m.ArticleID,
				m.Category,
				m.Label,
				m.Timestamp,
				m.Score,
			})
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin classification insert tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.CopyFrom(
		ctx,
		classificationTable,
		[]string{"id", "article_id", "timestamp", "data"},
		pgx.CopyFromRows(classificationRows),
	); err != nil {
		return fmt.Errorf("copy classifications into %s: %w", n, err)
	}

	if len(metricRows) > 0 {
		if _, err := tx.CopyFrom(
			ctx,
			metricsTable,
			[]string{"article_id", "category", "label", "timestamp", "score"},
			pgx.CopyFromRows(metricRows),
		); err != nil {
			return fmt.Errorf("copy metrics into %s_metrics: %w", n, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit classification insert tx: %w", err)
	}
	return nil
}
