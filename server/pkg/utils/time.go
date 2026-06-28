// pkg/utils/cron.go
package utils

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type cronSpec struct {
	name string
	min  int
	max  int
}

var (
	linuxFields = []cronSpec{
		{"minute", 0, 59},
		{"hour", 0, 23},
		{"day of month", 1, 31},
		{"month", 1, 12},
		{"day of week", 0, 6},
	}

	quartzFields = []cronSpec{
		{"second", 0, 59},
		{"minute", 0, 59},
		{"hour", 0, 23},
		{"day of month", 1, 31},
		{"month", 1, 12},
		{"day of week", 1, 7},
		{"year", 1970, 2099},
	}
)

// ValidateCronExpr accepts both 5-field (Linux) and 7-field (Quartz)
// cron expressions. Returns an error describing the first invalid
// field; nil when every field is in-range.
func ValidateCronExpr(expr string) error {
	chunks := strings.Fields(strings.TrimSpace(expr))

	switch len(chunks) {
	case 5:
		return validateFields(chunks, linuxFields)
	case 7:
		return validateFields(chunks, quartzFields)
	default:
		return fmt.Errorf("expected 5 or 7 fields, got %d", len(chunks))
	}
}

func validateFields(chunks []string, specs []cronSpec) error {
	for i, chunk := range chunks {
		if err := validateField(chunk, specs[i]); err != nil {
			return fmt.Errorf("%s: %w", specs[i].name, err)
		}
	}
	return nil
}

func validateField(field string, spec cronSpec) error {
	// Handle lists: "1,3,5"
	for part := range strings.SplitSeq(field, ",") {
		if err := validatePart(part, spec); err != nil {
			return err
		}
	}
	return nil
}

func validatePart(part string, spec cronSpec) error {
	// Split step: "*/5" or "1-10/2"
	base, step, hasStep := strings.Cut(part, "/")

	if err := validateBase(base, spec); err != nil {
		return err
	}

	if hasStep {
		stepVal, err := strconv.Atoi(step)
		if err != nil || stepVal < 1 {
			return fmt.Errorf("invalid step: %s", step)
		}
	}

	return nil
}

func validateBase(base string, spec cronSpec) error {
	// Wildcard
	if base == "*" {
		return nil
	}

	// Range: "1-5"
	if start, end, isRange := strings.Cut(base, "-"); isRange {
		startVal, err := strconv.Atoi(start)
		if err != nil {
			return fmt.Errorf("invalid range start: %s", start)
		}

		endVal, err := strconv.Atoi(end)
		if err != nil {
			return fmt.Errorf("invalid range end: %s", end)
		}

		if err := checkBounds(startVal, spec); err != nil {
			return err
		}
		if err := checkBounds(endVal, spec); err != nil {
			return err
		}
		if startVal > endVal {
			return fmt.Errorf("range start %d > end %d", startVal, endVal)
		}

		return nil
	}

	// Single value
	val, err := strconv.Atoi(base)
	if err != nil {
		return fmt.Errorf("invalid value: %s", base)
	}

	return checkBounds(val, spec)
}

func checkBounds(val int, spec cronSpec) error {
	if val < spec.min || val > spec.max {
		return fmt.Errorf("%d out of range (%d-%d)", val, spec.min, spec.max)
	}
	return nil
}

// UnixToTime converts a unix timestamp to time.Time, picking the
// right precision (micro / milli / second) from magnitude. Saves
// callers from guessing whether an upstream emitted seconds or
// milliseconds.
func UnixToTime(n int64) time.Time {
	switch {
	case n > 1e15:
		return time.UnixMicro(n)
	case n > 1e12:
		return time.UnixMilli(n)
	default:
		return time.Unix(n, 0)
	}
}
