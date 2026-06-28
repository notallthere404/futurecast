package utils

import (
	"strings"
	"testing"
	"time"
)

func TestUnixToTime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   int64
		want time.Time
	}{
		{name: "seconds", in: 1_700_000_000, want: time.Unix(1_700_000_000, 0)},
		{name: "milliseconds", in: 1_700_000_000_000, want: time.UnixMilli(1_700_000_000_000)},
		{name: "microseconds", in: 1_700_000_000_000_000, want: time.UnixMicro(1_700_000_000_000_000)},
		{name: "zero is seconds", in: 0, want: time.Unix(0, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := UnixToTime(tc.in)
			if !got.Equal(tc.want) {
				t.Errorf("UnixToTime(%d) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidateCronExpr_Valid(t *testing.T) {
	t.Parallel()
	valid := []string{
		"* * * * *",
		"0 0 * * *",
		"*/5 * * * *",
		"0 9-17 * * 1-5",
		"0,15,30,45 * * * *",
		"0 0 1 */2 *",
		"  * * * * *  ", // tolerates surrounding whitespace
		"*/30 * * * * * *",
	}
	for _, expr := range valid {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			if err := ValidateCronExpr(expr); err != nil {
				t.Errorf("ValidateCronExpr(%q) err = %v, want nil", expr, err)
			}
		})
	}
}

func TestValidateCronExpr_Invalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		msg  string
	}{
		{name: "wrong field count", expr: "* * *", msg: "expected 5 or 7 fields"},
		{name: "minute too high", expr: "60 * * * *", msg: "out of range"},
		{name: "minute negative", expr: "-1 * * * *", msg: "invalid range"},
		{name: "hour too high", expr: "0 24 * * *", msg: "out of range"},
		{name: "dom zero", expr: "0 0 0 * *", msg: "out of range"},
		{name: "month thirteen", expr: "0 0 1 13 *", msg: "out of range"},
		{name: "dow too high", expr: "0 0 * * 7", msg: "out of range"},
		{name: "bad range order", expr: "0 17-9 * * *", msg: "start"},
		{name: "non-numeric value", expr: "abc * * * *", msg: "invalid value"},
		{name: "non-numeric step", expr: "*/x * * * *", msg: "invalid step"},
		{name: "zero step", expr: "*/0 * * * *", msg: "invalid step"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateCronExpr(tc.expr)
			if err == nil {
				t.Fatalf("ValidateCronExpr(%q) err = nil, want error containing %q", tc.expr, tc.msg)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("ValidateCronExpr(%q) err = %q, want substring %q", tc.expr, err.Error(), tc.msg)
			}
		})
	}
}
