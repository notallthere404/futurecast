package db

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
)

func TestWhere_Empty(t *testing.T) {
	t.Parallel()
	w := NewWhere()
	clause, args := w.Build()
	if clause != "" {
		t.Errorf("empty Build clause = %q, want \"\"", clause)
	}
	if len(args) != 0 {
		t.Errorf("empty Build args = %v, want empty", args)
	}
}

func TestWhere_Add(t *testing.T) {
	t.Parallel()
	clause, args := NewWhere().
		Add("a = @a", "a", 1).
		Add("b = @b", "b", "x").
		Build()

	want := "WHERE a = @a AND b = @b"
	if clause != want {
		t.Errorf("clause = %q, want %q", clause, want)
	}
	if diff := cmp.Diff(pgx.NamedArgs{"a": 1, "b": "x"}, args); diff != "" {
		t.Errorf("args mismatch (-want +got):\n%s", diff)
	}
}

func TestWhere_AddIf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		cond       bool
		wantClause string
		wantArgs   pgx.NamedArgs
	}{
		{
			name:       "true keeps predicate",
			cond:       true,
			wantClause: "WHERE x = @x",
			wantArgs:   pgx.NamedArgs{"x": 7},
		},
		{
			name:       "false skips predicate",
			cond:       false,
			wantClause: "",
			wantArgs:   pgx.NamedArgs{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clause, args := NewWhere().AddIf(tc.cond, "x = @x", "x", 7).Build()
			if clause != tc.wantClause {
				t.Errorf("clause = %q, want %q", clause, tc.wantClause)
			}
			if diff := cmp.Diff(tc.wantArgs, args); diff != "" {
				t.Errorf("args mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWhere_AddRaw(t *testing.T) {
	t.Parallel()
	clause, args := NewWhere().AddRaw("deleted_at IS NULL").Build()
	if clause != "WHERE deleted_at IS NULL" {
		t.Errorf("clause = %q", clause)
	}
	if len(args) != 0 {
		t.Errorf("AddRaw must not add args, got %v", args)
	}
}

func TestWhere_AddArgs(t *testing.T) {
	t.Parallel()
	clause, args := NewWhere().
		AddArgs("score BETWEEN @lo AND @hi", pgx.NamedArgs{"lo": 1, "hi": 5}).
		Build()
	if clause != "WHERE score BETWEEN @lo AND @hi" {
		t.Errorf("clause = %q", clause)
	}
	if diff := cmp.Diff(pgx.NamedArgs{"lo": 1, "hi": 5}, args); diff != "" {
		t.Errorf("args mismatch (-want +got):\n%s", diff)
	}
}

func TestWhere_AddArgsIf(t *testing.T) {
	t.Parallel()
	w := NewWhere().
		AddArgsIf(false, "skip", pgx.NamedArgs{"a": 1}).
		AddArgsIf(true, "keep = @b", pgx.NamedArgs{"b": 2})
	clause, args := w.Build()
	if clause != "WHERE keep = @b" {
		t.Errorf("clause = %q", clause)
	}
	if diff := cmp.Diff(pgx.NamedArgs{"b": 2}, args); diff != "" {
		t.Errorf("args mismatch (-want +got):\n%s", diff)
	}
}

func TestWhere_Args_NoPredicate(t *testing.T) {
	t.Parallel()
	clause, args := NewWhere().Args(pgx.NamedArgs{"orderby": "id"}).Build()
	if clause != "" {
		t.Errorf("Args alone should not produce a clause, got %q", clause)
	}
	if diff := cmp.Diff(pgx.NamedArgs{"orderby": "id"}, args); diff != "" {
		t.Errorf("args mismatch (-want +got):\n%s", diff)
	}
}

func TestWhere_Chain(t *testing.T) {
	t.Parallel()
	clause, args := NewWhere().
		Add("a = @a", "a", 1).
		AddRaw("b IS NOT NULL").
		AddIf(true, "c = @c", "c", 3).
		AddIf(false, "d = @d", "d", 4).
		AddArgs("e IN (@e1, @e2)", pgx.NamedArgs{"e1": 5, "e2": 6}).
		Build()

	want := "WHERE a = @a AND b IS NOT NULL AND c = @c AND e IN (@e1, @e2)"
	if clause != want {
		t.Errorf("clause = %q\nwant      %q", clause, want)
	}
	if diff := cmp.Diff(pgx.NamedArgs{"a": 1, "c": 3, "e1": 5, "e2": 6}, args); diff != "" {
		t.Errorf("args mismatch (-want +got):\n%s", diff)
	}
}
