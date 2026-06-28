package source

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	v1 "github.com/notallthere404/futurecast/server/api/v1"

	"github.com/jackc/pgx/v5"
)

// sourceColList all columns scanned from the sources table. Auth /
// Extract / Retry / Headers jsonb cols round-trip via Scanner/Valuer
// methods on their Go types, so strict scan works (no Lax needed).
var sourceColList = []string{
	"id", "type", "name", "description", "tags", "dedupe_key",
	"timeout_seconds", "spec", "auth", "extract", "retry", "headers",
	"hash", "url", "created_at", "updated_at", "active", "trust",
}

var sourceCols = strings.Join(sourceColList, ", ")

// upsertCols subset written by inserts/upserts. Defaults handle id,
// created_at, updated_at.
var upsertCols = []string{
	"type", "name", "hash", "url", "spec",
	"auth", "extract", "retry", "headers",
	"description", "tags", "dedupe_key", "timeout_seconds",
	"active", "trust",
}

func specBytes(src *v1.Source) ([]byte, error) {
	if len(src.SpecRaw) > 0 {
		return src.SpecRaw, nil
	}
	if src.Spec == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(src.Spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	return b, nil
}

// jsonbOrNil marshal v to []byte for jsonb insertion; returns nil for
// nil pointers / empty maps so NULL lands in DB instead of "{}".
func jsonbOrNil(v any) ([]byte, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case *v1.Auth:
		if t == nil {
			return nil, nil
		}
	case *v1.Extract:
		if t == nil {
			return nil, nil
		}
	case *v1.Retry:
		if t == nil {
			return nil, nil
		}
	case v1.Headers:
		if len(t) == 0 {
			return nil, nil
		}
	}
	return json.Marshal(v)
}

// collectSources runs strict scan + HydrateSpec. Every Select* returns
// hydrated *v1.Source slices, so factor it.
func (s *Store) collectSources(rows pgx.Rows) ([]*v1.Source, error) {
	srcs, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[v1.Source])
	if err != nil {
		return nil, fmt.Errorf("collect sources: %w", err)
	}
	for _, src := range srcs {
		if err := src.HydrateSpec(); err != nil {
			return nil, fmt.Errorf("hydrate spec: %w", err)
		}
	}
	return srcs, nil
}

func (s *Store) SelectSourceByType(ctx context.Context, t v1.SourceType) ([]*v1.Source, error) {
	query := `SELECT ` + sourceCols + ` FROM sources WHERE active = true AND type = $1`

	rows, err := s.pool.Query(ctx, query, t)
	if err != nil {
		return nil, fmt.Errorf("select sources by type %q: %w", t, err)
	}
	return s.collectSources(rows)
}

func (s *Store) SelectSourceAll(ctx context.Context) ([]*v1.Source, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sourceCols+` FROM sources`)
	if err != nil {
		return nil, fmt.Errorf("select all sources: %w", err)
	}
	return s.collectSources(rows)
}

func (s *Store) DeleteSourceBatch(ctx context.Context, urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	query := `DELETE FROM sources WHERE url = ANY($1::text[])`
	if _, err := s.pool.Exec(ctx, query, urls); err != nil {
		return fmt.Errorf("delete sources: %w", err)
	}
	return nil
}

const upsertQuery = `
INSERT INTO sources (
	type, name, hash, url, spec,
	auth, extract, retry, headers,
	description, tags, dedupe_key, timeout_seconds,
	active, trust
)
VALUES (
	$1, $2, $3, $4, $5::jsonb,
	$6::jsonb, $7::jsonb, $8::jsonb, $9::jsonb,
	$10, $11, $12, $13,
	$14, $15
)
ON CONFLICT (url)
DO UPDATE SET
	type = EXCLUDED.type,
	name = EXCLUDED.name,
	hash = EXCLUDED.hash,
	spec = EXCLUDED.spec,
	auth = EXCLUDED.auth,
	extract = EXCLUDED.extract,
	retry = EXCLUDED.retry,
	headers = EXCLUDED.headers,
	description = EXCLUDED.description,
	tags = EXCLUDED.tags,
	dedupe_key = EXCLUDED.dedupe_key,
	timeout_seconds = EXCLUDED.timeout_seconds,
	updated_at = now(),
	active = EXCLUDED.active,
	trust = EXCLUDED.trust
`

func (s *Store) UpsertSource(ctx context.Context, src *v1.Source) error {
	row, err := upsertRow(src)
	if err != nil {
		return fmt.Errorf("build source upsert row: %w", err)
	}
	if _, err := s.pool.Exec(ctx, upsertQuery, row...); err != nil {
		return fmt.Errorf("upsert source: %w", err)
	}
	return nil
}

// UpsertSourceBatch bulk path: CopyFrom into a temp table, then merge
// with INSERT...SELECT...ON CONFLICT. One round trip for the copy and
// one for the merge, regardless of batch size.
func (s *Store) UpsertSourceBatch(ctx context.Context, srcs []*v1.Source) error {
	if len(srcs) == 0 {
		return nil
	}
	s.log.Debug("upserting sources", "count", len(srcs))

	rows := make([][]any, len(srcs))
	for i, src := range srcs {
		row, err := upsertRow(src)
		if err != nil {
			return err
		}
		rows[i] = row
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin source upsert tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE _sources_in (LIKE sources INCLUDING DEFAULTS) ON COMMIT DROP
	`); err != nil {
		return fmt.Errorf("create temp sources table: %w", err)
	}

	if _, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"_sources_in"},
		upsertCols,
		pgx.CopyFromRows(rows),
	); err != nil {
		return fmt.Errorf("copy sources into temp: %w", err)
	}

	if _, err := tx.Exec(ctx, `
	INSERT INTO sources (
		type, name, hash, url, spec,
		auth, extract, retry, headers,
		description, tags, dedupe_key, timeout_seconds,
		active, trust
	)
	SELECT
		type, name, hash, url, spec,
		auth, extract, retry, headers,
		description, tags, dedupe_key, timeout_seconds,
		active, trust
	FROM _sources_in
	ON CONFLICT (url) DO UPDATE SET
		type = EXCLUDED.type,
		name = EXCLUDED.name,
		hash = EXCLUDED.hash,
		spec = EXCLUDED.spec,
		auth = EXCLUDED.auth,
		extract = EXCLUDED.extract,
		retry = EXCLUDED.retry,
		headers = EXCLUDED.headers,
		description = EXCLUDED.description,
		tags = EXCLUDED.tags,
		dedupe_key = EXCLUDED.dedupe_key,
		timeout_seconds = EXCLUDED.timeout_seconds,
		updated_at = now(),
		active = EXCLUDED.active,
		trust = EXCLUDED.trust
	`); err != nil {
		return fmt.Errorf("merge sources from temp: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit source upsert tx: %w", err)
	}
	return nil
}

// upsertRow single source as ordered []any matching upsertCols.
// Jsonb cols pre-marshalled so CopyFrom (binary protocol) accepts them.
func upsertRow(src *v1.Source) ([]any, error) {
	spec, err := specBytes(src)
	if err != nil {
		return nil, err
	}
	auth, err := jsonbOrNil(src.Auth)
	if err != nil {
		return nil, fmt.Errorf("marshal auth: %w", err)
	}
	extract, err := jsonbOrNil(src.Extract)
	if err != nil {
		return nil, fmt.Errorf("marshal extract: %w", err)
	}
	retry, err := jsonbOrNil(src.Retry)
	if err != nil {
		return nil, fmt.Errorf("marshal retry: %w", err)
	}
	headers, err := jsonbOrNil(src.Headers)
	if err != nil {
		return nil, fmt.Errorf("marshal headers: %w", err)
	}
	return []any{
		src.Type, src.Name, src.Hash, src.URL, spec,
		auth, extract, retry, headers,
		src.Description, src.Tags, src.DedupeKey, src.TimeoutSeconds,
		src.Active, src.Trust,
	}, nil
}
