package utils

import (
	"regexp"
	"testing"
)

var uuidRegexp = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestNewUUIDv5_Deterministic(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
	}{
		{name: "url", input: "https://example.com/post/1"},
		{name: "empty", input: ""},
		{name: "unicode", input: "résumé"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := NewUUIDv5(tc.input)
			b := NewUUIDv5(tc.input)
			if a != b {
				t.Errorf("NewUUIDv5 not deterministic: %q vs %q", a, b)
			}
			if !uuidRegexp.MatchString(a) {
				t.Errorf("NewUUIDv5 not a uuid: %q", a)
			}
		})
	}
}

func TestNewUUIDv5_DifferentInputs(t *testing.T) {
	t.Parallel()
	if NewUUIDv5("a") == NewUUIDv5("b") {
		t.Error("distinct inputs collided")
	}
}

func TestNewUUIDv4_Unique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{}, 100)
	for range 100 {
		id := NewUuidv4()
		if !uuidRegexp.MatchString(id) {
			t.Fatalf("NewUuidv4 not a uuid: %q", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("collision: %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewArticleID(t *testing.T) {
	t.Parallel()

	t.Run("empty input is v4 (random)", func(t *testing.T) {
		t.Parallel()
		a := NewArticleID("")
		b := NewArticleID("")
		if a == b {
			t.Errorf("empty input should produce distinct ids, got %q twice", a)
		}
		if !uuidRegexp.MatchString(a) {
			t.Errorf("not a uuid: %q", a)
		}
	})

	t.Run("non-empty input is v5 (deterministic)", func(t *testing.T) {
		t.Parallel()
		link := "https://example.com/post/42"
		a := NewArticleID(link)
		b := NewArticleID(link)
		if a != b {
			t.Errorf("same link should produce same id, got %q vs %q", a, b)
		}
		if a != NewUUIDv5(link) {
			t.Errorf("expected NewArticleID(link) == NewUUIDv5(link)")
		}
	})
}
