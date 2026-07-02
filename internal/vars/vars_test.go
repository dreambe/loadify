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
