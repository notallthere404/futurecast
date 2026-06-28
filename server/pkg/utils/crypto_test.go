package utils

import (
	"testing"
)

func TestCreateHash(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a, b any
		eq   bool
	}{
		{name: "same string equal", a: "hello", b: "hello", eq: true},
		{name: "different string differ", a: "hello", b: "world", eq: false},
		{name: "same struct equal", a: struct{ X int }{1}, b: struct{ X int }{1}, eq: true},
		{name: "different struct differ", a: struct{ X int }{1}, b: struct{ X int }{2}, eq: false},
		{name: "slice order matters", a: []int{1, 2, 3}, b: []int{3, 2, 1}, eq: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ha, err := CreateHash(tc.a)
			if err != nil {
				t.Fatalf("CreateHash(a) err: %v", err)
			}
			hb, err := CreateHash(tc.b)
			if err != nil {
				t.Fatalf("CreateHash(b) err: %v", err)
			}
			got := ha == hb
			if got != tc.eq {
				t.Errorf("equal=%v, want %v\nha=%x\nhb=%x", got, tc.eq, ha, hb)
			}
		})
	}
}

func TestCreateHash_Deterministic(t *testing.T) {
	t.Parallel()
	for range 5 {
		h1, _ := CreateHash("payload")
		h2, _ := CreateHash("payload")
		if h1 != h2 {
			t.Fatalf("CreateHash not deterministic across calls")
		}
	}
}

func TestCompareHash(t *testing.T) {
	t.Parallel()
	a := []byte{1, 2, 3, 4}

	cases := []struct {
		name string
		a, b []byte
		want bool
	}{
		{name: "equal", a: a, b: []byte{1, 2, 3, 4}, want: true},
		{name: "differ", a: a, b: []byte{1, 2, 3, 5}, want: false},
		{name: "length mismatch", a: a, b: []byte{1, 2, 3}, want: false},
		{name: "both empty", a: nil, b: nil, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CompareHash(tc.a, tc.b); got != tc.want {
				t.Errorf("CompareHash = %v, want %v", got, tc.want)
			}
		})
	}
}
