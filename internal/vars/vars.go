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
	"sync/atomic"
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
// uuid, timestamp (unix ms), now (RFC3339), random (0-1), randomInt(a,b),
// randomFloat(a,b), randomString(n), randomDigits(n), randomHex(n),
// pick(a|b|c), seq (per-node counter), mobile, email, ipv4.
func builtin(t string) (string, bool) {
	name, args := t, ""
	if i := strings.IndexByte(t, '('); i >= 0 && strings.HasSuffix(t, ")") {
		name, args = t[:i], t[i+1:len(t)-1]
	}
	numArg := func(i int, def float64) float64 {
		parts := strings.Split(args, ",")
		if i >= len(parts) {
			return def
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(parts[i]), 64)
		if err != nil {
			return def
		}
		return v
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
		lo, hi := int64(numArg(0, 0)), int64(numArg(1, 0))
		if hi < lo {
			lo, hi = hi, lo
		}
		return strconv.FormatInt(lo+rand.Int64N(hi-lo+1), 10), true
	case "randomFloat":
		lo, hi := numArg(0, 0), numArg(1, 1)
		if hi < lo {
			lo, hi = hi, lo
		}
		return strconv.FormatFloat(lo+rand.Float64()*(hi-lo), 'f', -1, 64), true
	case "randomString":
		return RandomString(intArg(numArg(0, 8), 8)), true
	case "randomDigits":
		return RandomDigits(intArg(numArg(0, 6), 6)), true
	case "randomHex":
		return RandomHex(intArg(numArg(0, 16), 16)), true
	case "pick":
		opts := strings.Split(args, "|")
		if len(opts) == 0 {
			return "", true
		}
		return strings.TrimSpace(opts[rand.IntN(len(opts))]), true
	case "seq":
		return strconv.FormatUint(NextSeq(), 10), true
	case "mobile":
		return Mobile(), true
	case "email":
		return Email(), true
	case "ipv4":
		return IPv4(), true
	}
	return "", false
}

// intArg clamps a parsed numeric arg to a sane positive length.
func intArg(v float64, def int) int {
	n := int(v)
	if n <= 0 {
		return def
	}
	if n > 4096 {
		return 4096
	}
	return n
}

const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandomString returns n random alphanumeric characters.
func RandomString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alnum[rand.IntN(len(alnum))]
	}
	return string(b)
}

// RandomDigits returns n random decimal digits (e.g. verification codes).
func RandomDigits(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('0' + rand.IntN(10))
	}
	return string(b)
}

// RandomHex returns n random lowercase hex characters.
func RandomHex(n int) string {
	const hexdigits = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = hexdigits[rand.IntN(16)]
	}
	return string(b)
}

// Mobile returns a plausible Chinese mobile number (1[3-9] + 9 digits).
func Mobile() string {
	return "1" + strconv.Itoa(3+rand.IntN(7)) + RandomDigits(9)
}

// Email returns a random test-domain email address.
func Email() string {
	return strings.ToLower(RandomString(8)) + "@load.test"
}

// IPv4 returns a random dotted-quad address (octets 1-254).
func IPv4() string {
	oct := func() string { return strconv.Itoa(1 + rand.IntN(254)) }
	return oct() + "." + oct() + "." + oct() + "." + oct()
}

var seqCounter atomic.Uint64

// NextSeq returns a process-wide monotonically increasing integer (from 1) —
// unique within one worker node, not across nodes.
func NextSeq() uint64 {
	return seqCounter.Add(1)
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
