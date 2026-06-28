package view

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
	"github.com/notallthere404/futurecast/server/pkg/registry/db"

	"github.com/jackc/pgx/v5"
)

// viewColList all columns scanned from views. Panels jsonb scans into
// View.PanelsRaw; callers invoke HydratePanels after.
var viewColList = []string{
	"id", "slug", "title", "description", "user_id",
	"panels", "created_at", "updated_at",
}

var viewCols = strings.Join(viewColList, ", ")

func panelsBytes(v *v1.View) ([]byte, error) {
	if len(v.PanelsRaw) > 0 {
		return v.PanelsRaw, nil
	}
	if v.Panels == nil {
		return []byte("[]"), nil
	}
	b, err := json.Marshal(v.Panels)
	if err != nil {
		return nil, fmt.Errorf("marshal panels: %w", err)
	}
	return b, nil
}

// collectViews strict scan + HydratePanels. Mirrors source.collectSources.
func (s *Store) collectViews(rows pgx.Rows) ([]*v1.View, error) {
	views, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[v1.View])
	if err != nil {
		return nil, fmt.Errorf("collect views: %w", err)
	}
	for _, v := range views {
		if err := v.HydratePanels(); err != nil {
			return nil, fmt.Errorf("hydrate panels: %w", err)
		}
	}
	return views, nil
}

func (s *Store) SelectViewAll(ctx context.Context, userId *string) ([]*v1.View, error) {
	clause, args := db.NewWhere().
		AddIf(userId != nil, "user_id = @user_id", "user_id", userId).
		Build()

	query := fmt.Sprintf(`SELECT %s FROM views %s ORDER BY created_at DESC`, viewCols, clause)

	rows, err := s.pool.Query(ctx, query, args)
	if err != nil {
		return nil, fmt.Errorf("select views: %w", err)
	}
	return s.collectViews(rows)
}

func (s *Store) SelectViewBySlug(ctx context.Context, slug string) (*v1.View, error) {
	query := `SELECT ` + viewCols + ` FROM views WHERE slug = $1`

	rows, err := s.pool.Query(ctx, query, slug)
	if err != nil {
		return nil, fmt.Errorf("select view by slug: %w", err)
	}
	views, err := s.collectViews(rows)
	if err != nil {
		return nil, err
	}
	if len(views) == 0 {
		return nil, ErrNotFound
	}
	return views[0], nil
}

func (s *Store) DeleteViewBySlug(ctx context.Context, slug string) error {
	query := `DELETE FROM views WHERE slug = $1`
	if _, err := s.pool.Exec(ctx, query, slug); err != nil {
		return fmt.Errorf("delete view: %w", err)
	}
	return nil
}

const upsertQuery = `
INSERT INTO views (slug, title, description, user_id, panels)
VALUES ($1, $2, $3, $4, $5::jsonb)
ON CONFLICT (slug)
DO UPDATE SET
	title = EXCLUDED.title,
	description = EXCLUDED.description,
	user_id = EXCLUDED.user_id,
	panels = EXCLUDED.panels,
	updated_at = now()
`

func (s *Store) UpsertView(ctx context.Context, v *v1.View) error {
	row, err := upsertRow(v)
	if err != nil {
		return fmt.Errorf("build view upsert row: %w", err)
	}
	if _, err := s.pool.Exec(ctx, upsertQuery, row...); err != nil {
		return fmt.Errorf("upsert view: %w", err)
	}
	return nil
}

// upsertRow single view as ordered []any matching upsertCols.
func upsertRow(v *v1.View) ([]any, error) {
	panels, err := panelsBytes(v)
	if err != nil {
		return nil, err
	}
	return []any{v.Slug, v.Title, v.Description, v.UserID, panels}, nil
}

// ErrNotFound is exported so callers can distinguish "no row" from query failure.
var ErrNotFound = errors.New("view not found")
