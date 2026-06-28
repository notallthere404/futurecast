package classification

import (
	"context"
	"fmt"
	"strings"

	"github.com/notallthere404/futurecast/server/api/v1"
	"github.com/notallthere404/futurecast/server/pkg/registry/db"

	"github.com/jackc/pgx/v5"
)

func (s *Store) SelectScoreFrequency(ctx context.Context, name, label, start, end string) ([]*v1.LabelWeight, error) {
	metricsTable := pgx.Identifier{strings.ToLower(name) + "_metrics"}.Sanitize()

	whereClause, args := db.NewWhere().
		AddIf(start != "", "timestamp >= @start", "start", start).
		AddIf(end != "", "timestamp <= @end", "end", end).
		AddIf(label != "", "label = @label", "label", label).
		Build()

	query := fmt.Sprintf(`
    WITH ranged AS (
        SELECT timestamp::date AS day, score
        FROM %s
        %s
    ),
    totals AS (
        SELECT count(*)::float AS total FROM ranged
    )
    SELECT
        day,
        (count(*)::float / NULLIF(t.total, 0)) * AVG(score) AS value
    FROM ranged, totals t
    GROUP BY day, t.total
    ORDER BY day
    `, metricsTable, whereClause)

	rows, err := s.pool.Query(ctx, query, args)
	if err != nil {
		return nil, fmt.Errorf("select daily score frequency: %w", err)
	}
	defer rows.Close()

	results, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[v1.LabelWeight])
	if err != nil {
		return nil, fmt.Errorf("collect daily score frequency: %w", err)
	}

	return results, nil
}
