package metrics

import (
	"testing"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
)

func TestStatusClass(t *testing.T) {
	cases := []struct {
		status  int32
		ok      bool
		errKind string
		want    string
	}{
		{200, true, "", "2xx"},
		{204, true, "", "2xx"},
		{404, false, "", "4xx"},
		{503, false, "", "5xx"},
		{0, false, "timeout", "err"},
		{200, false, "unexpected_status", "err"},
	}
	for _, c := range cases {
		if got := StatusClass(c.status, c.ok, c.errKind); got != c.want {
			t.Errorf("StatusClass(%d,%v,%q)=%q want %q", c.status, c.ok, c.errKind, got, c.want)
		}
	}
}

func TestHistogramRoundTripAndMerge(t *testing.T) {
	// Two partial histograms that, merged, contain 1..1000.
	h1 := hdr.New(latencyMinUs, latencyMaxUs, sigFigures)
	h2 := hdr.New(latencyMinUs, latencyMaxUs, sigFigures)
	for v := int64(1); v <= 500; v++ {
		_ = h1.RecordValue(v * 1000) // store as µs; values are ms*1000
	}
	for v := int64(501); v <= 1000; v++ {
		_ = h2.RecordValue(v * 1000)
	}

	// Round-trip through encode/decode (simulating the wire).
	d1 := DecodeHistogram(EncodeHistogram(h1))
	d2 := DecodeHistogram(EncodeHistogram(h2))
	if d1 == nil || d2 == nil {
		t.Fatal("decode returned nil")
	}

	merged := hdr.New(latencyMinUs, latencyMaxUs, sigFigures)
	merged.Merge(d1)
	merged.Merge(d2)

	if merged.TotalCount() != 1000 {
		t.Fatalf("merged count = %d, want 1000", merged.TotalCount())
	}
	pct := PercentilesOf(merged)
	// p50 of 1..1000 ms ≈ 500ms, within histogram tolerance.
	if pct.P50 < 490 || pct.P50 > 510 {
		t.Errorf("p50 = %.1f ms, want ~500", pct.P50)
	}
	if pct.P99 < 980 || pct.P99 > 1000 {
		t.Errorf("p99 = %.1f ms, want ~990", pct.P99)
	}
}

func TestRecorder(t *testing.T) {
	r := NewRecorder()
	r.Record("g", 200, true, "", 1000, 10, 20)
	r.Record("g", 200, true, "", 2000, 10, 20)
	r.Record("g", 500, false, "", 3000, 10, 0)
	b := r.Buckets()
	if len(b) != 2 {
		t.Fatalf("want 2 status classes, got %d", len(b))
	}
	ok := b[Key{Group: "g", StatusClass: "2xx"}]
	if ok == nil || ok.Count != 2 || ok.Errors != 0 {
		t.Errorf("2xx bucket wrong: %+v", ok)
	}
	bad := b[Key{Group: "g", StatusClass: "5xx"}]
	if bad == nil || bad.Count != 1 || bad.Errors != 1 {
		t.Errorf("5xx bucket wrong: %+v", bad)
	}
}
