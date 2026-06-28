// Package db provides shared query helpers for registry stores.
package db

import (
	"strings"

	"github.com/jackc/pgx/v5"
)

// Where is a chainable WHERE clause builder. It accumulates AND-joined
// predicates and pgx.NamedArgs; Build returns the rendered clause
// (empty when no parts so callers can concatenate unconditionally).
type Where struct {
	parts []string
	args  pgx.NamedArgs
}

// NewWhere returns an empty Where ready for chained Add / AddIf calls.
func NewWhere() *Where {
	return &Where{args: pgx.NamedArgs{}}
}

// Add appends a predicate referencing one named arg.
//
//	w.Add("timestamp >= @start", "start", start)
func (w *Where) Add(predicate, name string, val any) *Where {
	w.parts = append(w.parts, predicate)
	w.args[name] = val
	return w
}

// AddIf appends the predicate only when cond is true. Lets callers
// skip optional filters without breaking the chain.
func (w *Where) AddIf(cond bool, predicate, name string, val any) *Where {
	if cond {
		w.Add(predicate, name, val)
	}
	return w
}

// AddRaw appends a predicate with no args, e.g. "deleted_at IS NULL".
func (w *Where) AddRaw(predicate string) *Where {
	w.parts = append(w.parts, predicate)
	return w
}

// AddArgs appends a predicate referencing multiple named args at once.
//
//	w.AddArgs("score BETWEEN @lo AND @hi", pgx.NamedArgs{"lo": 1, "hi": 5})
func (w *Where) AddArgs(predicate string, args pgx.NamedArgs) *Where {
	w.parts = append(w.parts, predicate)
	for k, v := range args {
		w.args[k] = v
	}
	return w
}

// AddArgsIf is AddArgs gated by cond. For multi-arg conditional predicates.
func (w *Where) AddArgsIf(cond bool, predicate string, args pgx.NamedArgs) *Where {
	if cond {
		w.AddArgs(predicate, args)
	}
	return w
}

// Args merges extra named args without adding a predicate. Useful when
// args feed parts of the query outside the WHERE clause (SELECT expr,
// JOIN condition) but should travel with the same arg map.
func (w *Where) Args(args pgx.NamedArgs) *Where {
	for k, v := range args {
		w.args[k] = v
	}
	return w
}

// Build renders the clause + args. Returns "" when empty so the caller
// can format unconditionally: fmt.Sprintf(`... FROM t %s ORDER BY ...`, clause).
func (w *Where) Build() (string, pgx.NamedArgs) {
	if len(w.parts) == 0 {
		return "", w.args
	}
	return "WHERE " + strings.Join(w.parts, " AND "), w.args
}
