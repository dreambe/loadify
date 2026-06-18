package metrics

import "testing"

// BenchmarkRecorderRecord measures the per-result recording hot path (every VU
// iteration on every worker goes through it). Run: go test -bench . ./internal/metrics
func BenchmarkRecorderRecord(b *testing.B) {
	rec := NewRecorder()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ok := i%10 != 0
		status := int32(200)
		if !ok {
			status = 500
		}
		rec.Record("default", status, ok, "", int64(i%50000)+1, 128, 256)
	}
}

// BenchmarkPercentiles measures percentile extraction from a populated bucket.
func BenchmarkPercentiles(b *testing.B) {
	rec := NewRecorder()
	for i := 0; i < 100000; i++ {
		rec.Record("default", 200, true, "", int64(i%50000)+1, 0, 0)
	}
	bk := rec.Buckets()
	var any *Bucket
	for _, v := range bk {
		any = v
		break
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = PercentilesOf(any.Hist)
	}
}
