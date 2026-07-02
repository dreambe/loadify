// Package vars implements {{...}} template interpolation for request fields,
// mirroring the scenario harness's JS _interp: literal variable lookup first,
// then built-in generators (uuid, timestamp, now, random, randomInt(a,b)),
// unknown tokens resolve to "". It powers per-request dynamic parameters for
// the plain-HTTP driver so a dataset row can feed URL/params/headers/body.
package vars

import (
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var token = regexp.MustCompile(`\{\{\s*([\w.]+(?:\([^)]*\))?)\s*\}\}`)

// Has reports whether s contains at least one {{...}} template token.
func Has(s string) bool {
	return strings.Contains(s, "{{") && token.MatchString(s)
}

// Interp substitutes {{name}} tokens from row (literal key lookup, matching the
// JS harness) and {{fn(...)}} built-ins. Unknown tokens become "".
func Interp(s string, row map[string]any) string {
	if s == "" || !strings.Contains(s, "{{") {
		return s
	}
	return token.ReplaceAllStringFunc(s, func(m string) string {
		t := strings.TrimSpace(m[2 : len(m)-2])
		if row != nil {
			if v, ok := row[t]; ok && v != nil {
				return stringify(v)
			}
		}
		if v, ok := builtin(t); ok {
			return v
		}
		return ""
	})
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		// JSON numbers decode to float64; print integers without the ".0".
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// builtin evaluates generator tokens, matching the scenario harness:
// uuid, timestamp (unix ms), now (RFC3339), random (0-1), randomInt(a,b).
func builtin(t string) (string, bool) {
	name, args := t, ""
	if i := strings.IndexByte(t, '('); i >= 0 && strings.HasSuffix(t, ")") {
		name, args = t[:i], t[i+1:len(t)-1]
	}
	switch name {
	case "uuid":
		return newUUID(), true
	case "timestamp":
		return strconv.FormatInt(time.Now().UnixMilli(), 10), true
	case "now":
		return time.Now().Format(time.RFC3339), true
	case "random":
		return strconv.FormatFloat(rand.Float64(), 'f', -1, 64), true
	case "randomInt":
		a, b := 0.0, 0.0
		parts := strings.Split(args, ",")
		if len(parts) > 0 {
			a, _ = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		}
		if len(parts) > 1 {
			b, _ = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		}
		lo, hi := int64(a), int64(b)
		if hi < lo {
			lo, hi = hi, lo
		}
		return strconv.FormatInt(lo+rand.Int64N(hi-lo+1), 10), true
	}
	return "", false
}

// newUUID returns a random RFC4122 v4 UUID string.
func newUUID() string {
	var b [16]byte
	_, _ = crand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	dst := make([]byte, 36)
	hex.Encode(dst, b[:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:], b[10:])
	return string(dst)
}
