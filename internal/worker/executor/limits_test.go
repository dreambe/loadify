package executor

import "testing"

func TestClampVUs(t *testing.T) {
	cases := []struct{ target, cap, want int }{
		{100, 5000, 100},   // under the cap
		{9000, 5000, 5000}, // clamped to the cap
		{9000, 0, 9000},    // cap disabled
		{0, 5000, 0},       // zero stays zero
	}
	for _, c := range cases {
		if got := clampVUs(c.target, c.cap); got != c.want {
			t.Errorf("clampVUs(%d,%d)=%d want %d", c.target, c.cap, got, c.want)
		}
	}
}
