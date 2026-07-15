package httpd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dreambe/loadify/internal/plan"
)

// evalAsserts runs the plan's structured assertions against a response.
// It returns "" when everything passes, or a short human-readable reason for
// the first failure (surfaced in the live response log's error column).
// Malformed bodies or missing fields fail the assertion — never panic.
func evalAsserts(asserts []plan.HTTPAssert, status int, body []byte) string {
	var parsed any
	var parsedOK, parsedTried bool
	for i := range asserts {
		a := &asserts[i]
		var actual any
		switch a.Source {
		case "status":
			actual = float64(status)
		case "body":
			actual = string(body)
		case "json":
			if !parsedTried {
				parsedTried = true
				parsedOK = json.Unmarshal(body, &parsed) == nil
			}
			if !parsedOK {
				return fmt.Sprintf("assert %s: body is not valid JSON", a.Path)
			}
			v, found := lookupPath(parsed, a.Path)
			if !found {
				if a.Op == "exists" {
					return fmt.Sprintf("assert %s: field missing", a.Path)
				}
				return fmt.Sprintf("assert %s %s %s: field missing", a.Path, a.Op, a.Value)
			}
			if a.Op == "exists" {
				continue
			}
			actual = v
		}
		if ok, got := compare(actual, a.Op, a.Value); !ok {
			name := a.Source
			if a.Source == "json" {
				name = a.Path
			}
			return fmt.Sprintf("assert %s %s %s: got %s", name, a.Op, a.Value, got)
		}
	}
	return ""
}

// lookupPath walks a decoded JSON value by dot notation; numeric segments
// index arrays ("data.items.0.id").
func lookupPath(v any, path string) (any, bool) {
	cur := v
	for _, seg := range strings.Split(path, ".") {
		if seg == "" {
			return nil, false
		}
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// compare applies op between the actual JSON value and the expected string,
// choosing numeric / boolean / string semantics from the actual value's type.
// It reports whether the assertion holds plus a printable form of the actual.
func compare(actual any, op, want string) (bool, string) {
	got := stringify(actual)
	switch op {
	case "contains":
		return strings.Contains(got, want), truncate(got)
	case "eq", "ne":
		eq := looseEqual(actual, want)
		if op == "ne" {
			return !eq, truncate(got)
		}
		return eq, truncate(got)
	case "gt", "lt", "gte", "lte":
		af, aok := toFloat(actual)
		wf, werr := strconv.ParseFloat(strings.TrimSpace(want), 64)
		if !aok || werr != nil {
			return false, truncate(got) + " (not a number)"
		}
		switch op {
		case "gt":
			return af > wf, truncate(got)
		case "lt":
			return af < wf, truncate(got)
		case "gte":
			return af >= wf, truncate(got)
		default:
			return af <= wf, truncate(got)
		}
	}
	return false, truncate(got)
}

// looseEqual compares by the actual value's type: numbers numerically,
// booleans as true/false, null against "null", everything else as strings.
func looseEqual(actual any, want string) bool {
	switch v := actual.(type) {
	case float64:
		if wf, err := strconv.ParseFloat(strings.TrimSpace(want), 64); err == nil {
			return v == wf
		}
		return false
	case bool:
		if wb, err := strconv.ParseBool(strings.TrimSpace(want)); err == nil {
			return v == wb
		}
		return false
	case nil:
		return strings.TrimSpace(want) == "null"
	default:
		return stringify(actual) == want
	}
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

func stringify(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(n)
	case nil:
		return "null"
	default:
		b, err := json.Marshal(n)
		if err != nil {
			return fmt.Sprintf("%v", n)
		}
		return string(b)
	}
}

// truncate caps an assertion's "actual" snippet at ~80 bytes, backing up to a
// rune boundary so a multi-byte character (e.g. Chinese in a JSON body) is never
// split into invalid UTF-8. The sampler sanitizes again before the wire, but
// keeping the snippet clean here avoids a trailing replacement char.
func truncate(s string) string {
	const cap = 80
	if len(s) <= cap {
		return s
	}
	b := s[:cap]
	for len(b) > 0 && !utf8.ValidString(b) {
		b = b[:len(b)-1]
	}
	return b + "…"
}
