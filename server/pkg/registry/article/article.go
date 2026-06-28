package article

import (
	"context"
	"fmt"
	"time"

	v1 "github.com/notallthere404/futurecast/server/api/v1"

	"github.com/jackc/pgx/v5"
)

// SelectArticleBatch - Method used for querying unprocessed articles.
func (s *Store) SelectArticleBatch(ctx context.Context, limit int) ([]*v1.ClassifyArticle, error) {
	query := `
	SELECT id, content, timestamp
	FROM articles
	WHERE
		processed = false
	ORDER BY
		timestamp DESC
	LIMIT $1
	`

	rows, err := s.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("select article batch: %w", err)
	}

	articles, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[v1.ClassifyArticle])
	if err != nil {
		return nil, fmt.Errorf("collect article batch: %w", err)
	}

	return articles, nil
}

// SelectArticleRecent - Method used for querying most recent fetched articles.
func (s *Store) SelectArticleRecent(ctx context.Context) ([]*v1.Article, error) {
	query := `
	SELECT id, source_id, source_type, title, content, timestamp, link, processed
	FROM articles
	ORDER BY
		timestamp DESC
	LIMIT 10
	`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("select recent articles: %w", err)
	}

	articles, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[v1.Article])
	if err != nil {
		return nil, fmt.Errorf("collect recent articles: %w", err)
	}

	return articles, nil
}

// SelectArticleRate - Method used for querying rate of article ingest.
func (s *Store) SelectArticleRate(ctx context.Context, format v1.RateFormat) ([]int, error) {
	var query string
	switch string(format) {
	case "day":
		query = `
		SELECT COALESCE(count(a.id), 0) AS cnt
		FROM generate_series(
			date_trunc('hour', $1::timestamptz) - interval '23 hours',
			date_trunc('hour', $1::timestamptz),
			interval '1 hour'
			) AS gs
		LEFT JOIN articles a
		ON a.timestamp >= gs
		AND a.timestamp < gs + interval '1 hour'
		GROUP BY gs
		ORDER BY gs;
		`
	case "month":
		query = `
		SELECT COALESCE(count(a.id), 0) AS cnt
		FROM generate_series(
			date_trunc('day', $1::timestamptz) - interval '29 days',
			date_trunc('day', $1::timestamptz),
			interval '1 day'
			) AS gs
		LEFT JOIN articles a
		ON a.timestamp >= gs
		AND a.timestamp < gs + interval '1 day'
		GROUP BY gs
		ORDER BY gs;
		`
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}

	rows, err := s.pool.Query(ctx, query, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("select article rate: %w", err)
	}

	counts, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return nil, fmt.Errorf("collect article rate: %w", err)
	}
	return counts, nil
}

func (s *Store) InsertArticleBatch(ctx context.Context, articles []*v1.Article) error {
	if len(articles) == 0 {
		return nil
	}
	s.log.Debug("inserting articles", "count", len(articles))

	rows := make([][]any, len(articles))
	for i, a := range articles {
		rows[i] = []any{
			a.ID,
			a.SourceID,
			string(a.SourceType),
			a.Title,
			a.Content,
			a.Timestamp,
			a.Link,
			a.Processed,
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin article insert tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE _articles_in (LIKE articles INCLUDING DEFAULTS) ON COMMIT DROP
	`); err != nil {
		return fmt.Errorf("create temp articles table: %w", err)
	}

	if _, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"_articles_in"},
		[]string{"id", "source_id", "source_type", "title", "content", "timestamp", "link", "processed"},
		pgx.CopyFromRows(rows),
	); err != nil {
		return fmt.Errorf("copy articles into temp: %w", err)
	}

	if _, err := tx.Exec(ctx, `
	INSERT INTO articles (id, source_id, source_type, title, content, timestamp, link, processed)
	SELECT id, source_id, source_type, title, content, timestamp, link, processed
	FROM _articles_in
	ON CONFLICT (id) DO NOTHING
	`); err != nil {
		return fmt.Errorf("merge articles from temp: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit article insert tx: %w", err)
	}

	s.log.Info("articles inserted")
	return nil
}

// UpdateArticleProcessed - Single-statement update over all ids via ANY array.
func (s *Store) UpdateArticleProcessed(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	query := `
		UPDATE articles
		SET processed = true
		WHERE id = ANY($1::uuid[])
	`
	if _, err := s.pool.Exec(ctx, query, ids); err != nil {
		return fmt.Errorf("update articles processed: %w", err)
	}
	return nil
}
