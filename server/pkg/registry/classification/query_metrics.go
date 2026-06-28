package classification

import (
	"context"
	"fmt"
	"strings"

	"github.com/notallthere404/futurecast/server/api/v1"
	"github.com/notallthere404/futurecast/server/pkg/registry/db"

	"github.com/jackc/pgx/v5"
)

func (s *Store) SelectFilteredClassMetric(ctx context.Context, name string, labels []string, start, end string) ([]*v1.MetricAgg, int, error) {
	n := strings.ToLower(name)
	metricsTable := pgx.Identifier{n + "_metrics"}.Sanitize()

	clause, args := db.NewWhere().
		AddIf(start != "", "timestamp >= @start", "start", start).
		AddIf(end != "", "timestamp <= @end", "end", end).
		AddIf(len(labels) > 0, "label = ANY(@labels)", "labels", labels).
		Build()

	query := fmt.Sprintf(`
	SELECT
		label,
		count(score) as n,
		avg(score) as mean,
		var_pop(score) as var
	FROM %s
	%s
	GROUP BY label
	`, metricsTable, clause)

	rows, err := s.pool.Query(ctx, query, args)
	if err != nil {
		return nil, -1, fmt.Errorf("select class metrics: %w", err)
	}

	met, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByNameLax[v1.MetricAgg])
	if err != nil {
		return nil, -1, fmt.Errorf("collect class metrics: %w", err)
	}

	total := 0
	for _, m := range met {
		total += m.Count
	}

	return met, total, nil
}
