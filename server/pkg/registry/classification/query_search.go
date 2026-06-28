package classification

import (
	"context"
	"fmt"
	"strings"

	"github.com/notallthere404/futurecast/server/api/v1"
	"github.com/notallthere404/futurecast/server/pkg/registry/db"

	"github.com/jackc/pgx/v5"
)

func (s *Store) SelectClassificationCount(ctx context.Context, name, start, end string) (int, error) {
	table := pgx.Identifier{strings.ToLower(name)}.Sanitize()

	clause, args := db.NewWhere().
		AddIf(start != "", "timestamp >= @start", "start", start).
		AddIf(end != "", "timestamp <= @end", "end", end).
		Build()

	query := fmt.Sprintf(`
		SELECT count(*)
		FROM %s
		%s
	`, table, clause)

	var count int
	if err := s.pool.QueryRow(ctx, query, args).Scan(&count); err != nil {
		return 0, fmt.Errorf("count classifications: %w", err)
	}
	return count, nil
}

func (s *Store) SelectFilteredClassification(ctx context.Context, name, title string, labels []string, start, end string, cutoff float64, limit int) ([]*v1.LinkedClassification, error) {
	n := strings.ToLower(name)
	table := pgx.Identifier{n}.Sanitize()
	metricsTable := pgx.Identifier{n + "_metrics"}.Sanitize()

	// Filter LabelScores within JSONB by score >= cutoff, drop empty attribute arrays.
	dataExpr := "c.data"
	w := db.NewWhere()
	if cutoff > 0 {
		dataExpr = `(
			SELECT COALESCE(jsonb_object_agg(sub.key, sub.vals), '{}'::jsonb)
			FROM (
				SELECT j.key, jsonb_agg(elem) AS vals
				FROM jsonb_each(c.data) AS j(key, value),
				     jsonb_array_elements(j.value) AS elem
				WHERE (elem->>'score')::float >= @cutoff
				GROUP BY j.key
			) sub
		)`
		w.Args(pgx.NamedArgs{"cutoff": cutoff})
	}

	w.AddIf(title != "", "a.title ILIKE @title", "title", "%"+title+"%").
		AddIf(start != "", "c.timestamp >= @start", "start", start).
		AddIf(end != "", "c.timestamp <= @end", "end", end).
		AddArgsIf(len(labels) > 0, fmt.Sprintf(`c.article_id IN (
			SELECT article_id
			FROM %s
			WHERE label = ANY(@labels)
			GROUP BY article_id
			HAVING COUNT(DISTINCT label) = @label_count
			ORDER BY SUM(score) DESC
		)`, metricsTable), pgx.NamedArgs{"labels": labels, "label_count": len(labels)})

	clause, args := w.Build()

	inner := fmt.Sprintf(`
	SELECT DISTINCT ON (c.article_id)
	       c.id, c.article_id, c.timestamp, %s AS data,
	       a.title, a.link
	FROM %s c
	JOIN articles a ON a.id = c.article_id
	%s
	ORDER BY c.article_id, c.timestamp DESC
	`, dataExpr, table, clause)

	// Drop articles whose data became empty after cutoff filter.
	query := `
		SELECT * FROM (` + inner + `) sub
		WHERE data IS NOT NULL AND data <> '{}'::jsonb
		ORDER BY timestamp DESC
		LIMIT @limit
	`
	args["limit"] = limit

	rows, err := s.pool.Query(ctx, query, args)
	if err != nil {
		return nil, fmt.Errorf("search classifications: %w", err)
	}

	results, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[v1.LinkedClassification])
	if err != nil {
		return nil, fmt.Errorf("collect classifications: %w", err)
	}

	return results, nil
}
