package vars

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestInterpRowLookup(t *testing.T) {
	row := map[string]any{
		"user":   "alice",
		"id":     float64(42), // JSON numbers decode to float64
		"flag":   true,
		"nested": map[string]any{"a": 1},
	}
	got := Interp("u={{user}}&id={{ id }}&f={{flag}}&n={{nested}}&miss={{nope}}", row)
	want := `u=alice&id=42&f=true&n={"a":1}&miss=`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestInterpBuiltins(t *testing.T) {
	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidRe.MatchString(Interp("{{uuid}}", nil)) {
		t.Fatalf("uuid: got %q", Interp("{{uuid}}", nil))
	}
	if ts, err := strconv.ParseInt(Interp("{{timestamp}}", nil), 10, 64); err != nil || ts < 1e12 {
		t.Fatalf("timestamp: %v %v", ts, err)
	}
	if !strings.Contains(Interp("{{now}}", nil), "T") {
		t.Fatalf("now: got %q", Interp("{{now}}", nil))
	}
	for i := 0; i < 100; i++ {
		n, err := strconv.Atoi(Interp("{{randomInt(3,5)}}", nil))
		if err != nil || n < 3 || n > 5 {
			t.Fatalf("randomInt out of range: %d %v", n, err)
		}
	}
	// A row variable wins over a builtin of the same name.
	if got := Interp("{{uuid}}", map[string]any{"uuid": "fixed"}); got != "fixed" {
		t.Fatalf("row should shadow builtin, got %q", got)
	}
}

func TestInterpGenerators(t *testing.T) {
	if got := Interp("{{randomString(12)}}", nil); len(got) != 12 || !regexp.MustCompile(`^[A-Za-z0-9]+$`).MatchString(got) {
		t.Fatalf("randomString: %q", got)
	}
	if got := Interp("{{randomDigits(6)}}", nil); len(got) != 6 || !regexp.MustCompile(`^\d+$`).MatchString(got) {
		t.Fatalf("randomDigits: %q", got)
	}
	if got := Interp("{{randomHex(8)}}", nil); len(got) != 8 || !regexp.MustCompile(`^[0-9a-f]+$`).MatchString(got) {
		t.Fatalf("randomHex: %q", got)
	}
	if got := Interp("{{mobile}}", nil); !regexp.MustCompile(`^1[3-9]\d{9}$`).MatchString(got) {
		t.Fatalf("mobile: %q", got)
	}
	if got := Interp("{{email}}", nil); !strings.HasSuffix(got, "@load.test") {
		t.Fatalf("email: %q", got)
	}
	if got := Interp("{{ipv4}}", nil); !regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`).MatchString(got) {
		t.Fatalf("ipv4: %q", got)
	}
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		v := Interp("{{pick(a|b|c)}}", nil)
		if v != "a" && v != "b" && v != "c" {
			t.Fatalf("pick: %q", v)
		}
		seen[v] = true
	}
	if len(seen) < 2 {
		t.Fatal("pick should vary")
	}
	a, _ := strconv.Atoi(Interp("{{seq}}", nil))
	b, _ := strconv.Atoi(Interp("{{seq}}", nil))
	if b != a+1 {
		t.Fatalf("seq not increasing: %d then %d", a, b)
	}
	for i := 0; i < 50; i++ {
		f, err := strconv.ParseFloat(Interp("{{randomFloat(1,2)}}", nil), 64)
		if err != nil || f < 1 || f >= 2 {
			t.Fatalf("randomFloat out of range: %v %v", f, err)
		}
	}
}

func TestInterpNoTemplates(t *testing.T) {
	s := "https://example.com/path?a=1"
	if got := Interp(s, nil); got != s {
		t.Fatalf("unchanged expected, got %q", got)
	}
	if Has(s) {
		t.Fatal("Has should be false")
	}
	if !Has("x {{user}} y") {
		t.Fatal("Has should be true")
	}
}
