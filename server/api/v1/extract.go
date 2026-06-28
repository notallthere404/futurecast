package v1

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/notallthere404/futurecast/server/pkg/utils"
)

// ExtractString walk dot-path in data, return leaf if string.
// Used by webhook/http drivers to map payload fields into article shape.
func ExtractString(key string, data any) (string, bool) {
	v, ok := keyWalk(key, data)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// ExtractTime walk dot-path, parse RFC3339 string or unix-seconds number.
func ExtractTime(key string, data any) (time.Time, bool) {
	v, ok := keyWalk(key, data)
	if !ok {
		return time.Time{}, false
	}

	switch t := v.(type) {
	case string:
		ts, err := time.Parse(time.RFC3339, t)
		return ts, err == nil
	case float64:
		return utils.UnixToTime(int64(t)), true
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return time.Time{}, false
		}
		return utils.UnixToTime(n), true
	}

	return time.Time{}, false
}

// keyWalk descends the dot-separated key path through nested maps.
// Assertion before descent, value fetch after; loop exits when keys
// run out so the leaf never enters another assertion.
func keyWalk(key string, data any) (any, bool) {
	tmp := data
	for _, k := range strings.Split(key, ".") {
		m, ok := tmp.(map[string]any)
		if !ok {
			return nil, false
		}
		tmp, ok = m[k]
		if !ok {
			return nil, false
		}
	}
	return tmp, true
}
