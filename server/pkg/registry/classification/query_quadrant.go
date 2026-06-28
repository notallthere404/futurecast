package classification

import (
	"context"
	"fmt"
	"strings"

	"github.com/notallthere404/futurecast/server/api/v1"
	"github.com/notallthere404/futurecast/server/pkg/registry/db"

	"github.com/jackc/pgx/v5"
)

func (s *Store) SelectLabelDelta(ctx context.Context, name, label, start, end string) (*v1.LabelFrequencyAverage, error) {
	metricsTable := pgx.Identifier{strings.ToLower(name) + "_metrics"}.Sanitize()

	clause, args := db.NewWhere().
		Add("label = @label", "label", label).
		AddIf(start != "", "timestamp >= @start", "start", start).
		AddIf(end != "", "timestamp <= @end", "end", end).
		Build()

	query := fmt.Sprintf(`
		SELECT
			@label::text AS label,
			COUNT(*)::float AS frequency,
			COALESCE(AVG(score), 0)::float AS mean_confidence
		FROM %s
		%s
	`, metricsTable, clause)

	freqavg := &v1.LabelFrequencyAverage{}
	if err := s.pool.QueryRow(ctx, query, args).Scan(
		&freqavg.Label,
		&freqavg.Frequency,
		&freqavg.MeanConfidence,
	); err != nil {
		return nil, fmt.Errorf("select label freq/mean: %w", err)
	}

	return freqavg, nil
}
