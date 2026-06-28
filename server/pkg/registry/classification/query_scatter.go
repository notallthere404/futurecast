package classification

import (
	"context"
	"fmt"
	"strings"

	"github.com/notallthere404/futurecast/server/api/v1"
	"github.com/notallthere404/futurecast/server/pkg/registry/db"

	"github.com/jackc/pgx/v5"
)

func (s *Store) SelectFilteredMetric(ctx context.Context, name, attribute, start, end string, labels []string, cutoff float64) ([]*v1.MetricScatter, error) {
	table := pgx.Identifier{strings.ToLower(name) + "_metrics"}.Sanitize()

	clause, args := db.NewWhere().
		AddIf(attribute != "", "m.category = @category", "category", attribute).
		AddIf(cutoff > 0, "m.score >= @cutoff", "cutoff", cutoff).
		AddIf(start != "", "a.timestamp >= @start", "start", start).
		AddIf(end != "", "a.timestamp <= @end", "end", end).
		AddIf(len(labels) > 0, "m.label = ANY(@labels)", "labels", labels).
		Build()

	query := fmt.Sprintf(`
		SELECT m.article_id, a.title, a.link, m.label, m.timestamp, m.score
		FROM %s m
		JOIN articles a ON a.id = m.article_id
		%s
	`, table, clause)

	rows, err := s.pool.Query(ctx, query, args)
	if err != nil {
		return nil, fmt.Errorf("select scatter metrics: %w", err)
	}

	results, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[v1.MetricScatter])
	if err != nil {
		return nil, fmt.Errorf("collect scatter metrics: %w", err)
	}

	return results, nil
}
