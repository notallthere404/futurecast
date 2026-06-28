package monitor

import (
	"context"
	"fmt"
	"time"

	"github.com/notallthere404/futurecast/server/api/v1"

	"github.com/jackc/pgx/v5"
)

func (s *Store) UpsertUptimeEntry(ctx context.Context, threshold int) (int, error) {
	var id int
	if err := s.pool.QueryRow(ctx,
		`SELECT upsert_monitor_uptime($1)`,
		threshold,
	).Scan(&id); err != nil {
		return 0, err
	} else {
		return id, nil
	}
}

func (s *Store) SelectUptimeTotal(ctx context.Context, start, end string) (float64, error) {
	query := `
    SELECT
      COALESCE(
        SUM(EXTRACT(EPOCH FROM (LEAST(recent, @end) - GREATEST(start, @start)))
        ), 0
      )
      / NULLIF(EXTRACT(EPOCH FROM (@end - @start)), 0) AS uptime_percent
    FROM monitor_uptime
    WHERE recent > @start
      AND start < @end;
    `

	args := pgx.NamedArgs{
		"start": start,
		"end":   end,
	}

	var percent float64
	err := s.pool.QueryRow(ctx, query, args).Scan(&percent)
	if err != nil {
		return 0, err
	}

	if percent < 0 {
		percent = 0
	}
	if percent > 1 {
		percent = 1
	}
	return percent, nil
}

func (s *Store) SelectUptimeSegment(ctx context.Context, format v1.RateFormat) ([]float64, error) {
	var (
		step    string
		buckets int
	)

	switch string(format) {
	case "day":
		step = "10 minutes"
		buckets = 144
	case "month":
		step = "1 day"
		buckets = 30
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}

	query := `
	WITH buckets AS (
			SELECT
				gs AS bucket_start,
				gs + $2::interval AS bucket_end
			FROM generate_series(
				$1::timestamptz - ($3::int * $2::interval),
				$1::timestamptz - $2::interval,
				$2::interval
			) AS gs
		)
		SELECT
			COALESCE(
				100.0 * SUM(
					CASE
						WHEN u.id IS NULL THEN 0
						ELSE EXTRACT(EPOCH FROM LEAST(b.bucket_end, u.recent) - GREATEST(b.bucket_start, u.start))
					END
				) / NULLIF(EXTRACT(EPOCH FROM b.bucket_end - b.bucket_start), 0),
				0
			) AS percent
	FROM buckets b
	LEFT JOIN monitor_uptime u
		ON u.up = true
	AND u.start < b.bucket_end
	AND u.recent > b.bucket_start
	GROUP BY b.bucket_start, b.bucket_end
	ORDER BY b.bucket_start;
	`

	rows, err := s.pool.Query(ctx, query, time.Now(), step, buckets)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[float64])
}
