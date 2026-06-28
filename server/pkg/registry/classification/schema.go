package classification

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Store) SelectTableAll(ctx context.Context) (map[string]struct{}, error) {
	query := `
		SELECT tablename AS table_name
		FROM pg_catalog.pg_tables
		WHERE schemaname = 'public'
			AND tablename NOT IN ('sources', 'source_urls', 'articles', 'monitor_uptime', 'views')
			AND tablename NOT LIKE '%\_metrics' ESCAPE '\'
		ORDER BY tablename
		`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("select classification tables: %w", err)
	}
	defer rows.Close()

	tables := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables[name] = struct{}{}
	}

	return tables, nil
}

func (s *Store) SelectDataKeyAll(ctx context.Context, name string) (map[string]struct{}, error) {
	table := pgx.Identifier{strings.ToLower(name)}.Sanitize()

	query := "SELECT DISTINCT jsonb_object_keys(data) FROM " + table

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := make(map[string]struct{})
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys[key] = struct{}{}
	}
	return keys, nil
}

// UpdateDataKeyAll performs a bulk update to add/remove JSONB keys in one shot.
func (s *Store) UpdateDataKeyAll(ctx context.Context, name string, add, remove []string) error {
	table := pgx.Identifier{strings.ToLower(name)}.Sanitize()

	query := fmt.Sprintf("UPDATE %s SET data = data", table)
	args := []any{}
	idx := 1

	if len(remove) > 0 {
		query += fmt.Sprintf(" - $%d::text[]", idx)
		args = append(args, remove)
		idx++
	}

	if len(add) > 0 {
		defaults := make(map[string]any, len(add))
		for _, k := range add {
			defaults[k] = []map[string]any{{"label": "n/i", "score": 0.0}}
		}
		query += fmt.Sprintf(" || $%d::jsonb", idx)
		args = append(args, defaults)
	}

	_, err := s.pool.Exec(ctx, query, args...)
	return err
}

func (s *Store) CreateTable(ctx context.Context, name string) error {
	lower := strings.ToLower(name)
	table := pgx.Identifier{lower}.Sanitize()
	table2 := pgx.Identifier{lower + "_metrics"}.Sanitize()

	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id uuid PRIMARY KEY NOT NULL DEFAULT gen_random_uuid(),
			article_id uuid NOT NULL REFERENCES articles(id) ON DELETE NO ACTION,
			timestamp timestamptz NOT NULL,
			data jsonb
		)`, table)

	query2 := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id SERIAL,
			article_id uuid NOT NULL REFERENCES articles(id) ON DELETE NO ACTION,
			category varchar(255) NOT NULL,
			label varchar(255) NOT NULL,
			timestamp timestamptz NOT NULL,
			score real NOT NULL
		)`, table2)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, query); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, query2); err != nil {
		return err
	}

	idx := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s(timestamp DESC)`,
		pgx.Identifier{lower + "_timestamp_idx"}.Sanitize(), table)

	if _, err := tx.Exec(ctx, idx); err != nil {
		return err
	}

	idx2 := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s USING brin(timestamp)`,
		pgx.Identifier{lower + "_metrics_timestamp_brin_idx"}.Sanitize(), table2)
	idx3 := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s(label)`,
		pgx.Identifier{lower + "_metrics_label_idx"}.Sanitize(), table2)

	if _, err := tx.Exec(ctx, idx2); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, idx3); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Store) DropTable(ctx context.Context, name string) error {
	lower := strings.ToLower(name)
	table := pgx.Identifier{lower}.Sanitize()
	tableMetrics := pgx.Identifier{lower + "_metrics"}.Sanitize()

	query := fmt.Sprintf(`
		DROP TABLE %s, %s
	`, tableMetrics, table)

	_, err := s.pool.Exec(ctx, query)
	if err != nil {
		return err
	}

	return nil
}
