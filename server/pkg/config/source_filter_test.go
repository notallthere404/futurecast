package config

import (
	"strings"
	"testing"
)

// stubDoc minimal Doc for tests. Wildcard expansion uses Keys() order.
type stubDoc map[string]string

func (d stubDoc) Field(name string) (string, bool) {
	v, ok := d[name]
	return v, ok
}

func (d stubDoc) Fields() []string {
	out := make([]string, 0, len(d))
	for k := range d {
		out = append(out, k)
	}
	return out
}

func TestParseFilter(t *testing.T) {
	cases := []struct {
		in      string
		want    Filter
		wantErr bool
	}{
		{
			in:   "content.len.gte.200",
			want: Filter{Target: "content", Generator: GenLen, Operator: OpGte, Value: "200"},
		},
		{
			in:   "title.regex.eq.^CVE-",
			want: Filter{Target: "title", Generator: GenRegex, Operator: OpEq, Value: "^CVE-"},
		},
		{
			in:   "score.gte.0.85",
			want: Filter{Target: "score", Generator: GenSelf, Operator: OpGte, Value: "0.85"},
		},
		{
			in:   "!tags.contains.eq.spam",
			want: Filter{Negate: true, Target: "tags", Generator: GenContains, Operator: OpEq, Value: "spam"},
		},
		{
			in:   ".len.gte.10",
			want: Filter{Target: "*", Generator: GenLen, Operator: OpGte, Value: "10"},
		},
		{
			// Short form: gen.op.value with target defaulting to wildcard.
			// Used in the common config form `len.gte.100` without
			// explicitly spelling out a target.
			in:   "len.gte.10",
			want: Filter{Target: "*", Generator: GenLen, Operator: OpGte, Value: "10"},
		},
		{
			in:   "contains.eq.cyber",
			want: Filter{Target: "*", Generator: GenContains, Operator: OpEq, Value: "cyber"},
		},
		{
			in:      "no_dots",
			wantErr: true,
		},
		{
			in:      "title.unknownop.foo",
			wantErr: true,
		},
		{
			in:      "title.unknowngen.eq.foo",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseFilter(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want err, got nil; parsed=%+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %+v, got %+v", tc.want, got)
			}
		})
	}
}

func TestParseFilters(t *testing.T) {
	in := []string{"content.len.gte.10", "title.contains.eq.alert"}
	out, err := ParseFilters(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 filters, got %d", len(out))
	}
}

func TestFilterEval(t *testing.T) {
	doc := stubDoc{
		"title":   "CVE-2026-1234",
		"content": strings.Repeat("x", 250),
		"score":   "0.91",
		"tags":    "alert,advisory",
	}

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"len pass", "content.len.gte.200", true},
		{"len fail", "content.len.gte.1000", false},
		{"regex hit", "title.regex.eq.^CVE-", true},
		{"regex miss", "title.regex.eq.^FOO-", false},
		{"contains hit", "tags.contains.eq.alert", true},
		{"contains miss", "tags.contains.eq.spam", false},
		{"negated miss = pass", "!tags.contains.eq.spam", true},
		{"float gte string-compare", "score.gte.0.85", true}, // GenSelf = lexicographic compare; "0.91" > "0.85"
		{"string eq", "title.eq.CVE-2026-1234", true},
		{"empty hit", "missing.empty.eq.true", false}, // missing field never matches
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := ParseFilter(tc.in)
			if err != nil {
				t.Fatalf("parse err: %v", err)
			}
			got, err := f.Eval(doc)
			if err != nil {
				t.Fatalf("eval err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("%s: want %v, got %v", tc.in, tc.want, got)
			}
		})
	}
}

func TestFilterEvalWildcard(t *testing.T) {
	doc := stubDoc{
		"title":   "hello world",
		"content": "short",
	}
	// "*"; any field length >= 5 → title (11) hits first
	f, err := ParseFilter(".len.gte.5")
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	got, err := f.Eval(doc)
	if err != nil {
		t.Fatalf("eval err: %v", err)
	}
	if !got {
		t.Fatalf("want true for wildcard match")
	}
}
