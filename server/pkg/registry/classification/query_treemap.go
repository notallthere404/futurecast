package classification

import (
	"context"
	"fmt"
	"strings"

	"github.com/notallthere404/futurecast/server/api/v1"
	"github.com/notallthere404/futurecast/server/pkg/registry/db"

	"github.com/jackc/pgx/v5"
)

func (s *Store) SelectLabelCounts(ctx context.Context, name, attribute, start, end string, cutoff float64) ([]*v1.LabelCount, error) {
	metricsTable := pgx.Identifier{strings.ToLower(name) + "_metrics"}.Sanitize()

	clause, args := db.NewWhere().
		Add("score >= @cutoff", "cutoff", cutoff).
		AddIf(attribute != "", "category = @category", "category", attribute).
		AddIf(start != "", "timestamp >= @start", "start", start).
		AddIf(end != "", "timestamp <= @end", "end", end).
		Build()

	query := fmt.Sprintf(`
		SELECT label, count(*) AS count
		FROM %s
		%s
		GROUP BY label
		ORDER BY count(*) DESC
	`, metricsTable, clause)

	rows, err := s.pool.Query(ctx, query, args)
	if err != nil {
		return nil, fmt.Errorf("select label counts: %w", err)
	}

	results, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[v1.LabelCount])
	if err != nil {
		return nil, fmt.Errorf("collect label counts: %w", err)
	}

	return results, nil
}
