package httpd

import (
	"sync"
	"testing"
	"time"
)

// TestPhaseTimingsConcurrent exercises the lock-free phase recorder the way
// httptrace does: callbacks firing from several goroutines. Run with -race.
func TestPhaseTimingsConcurrent(t *testing.T) {
	ph := &phase{}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		ph.markStart(&ph.dnsStart)
		time.Sleep(time.Millisecond)
		ph.markDone(&ph.dnsStart, &ph.dnsUs)
	}()
	go func() {
		defer wg.Done()
		ph.markStart(&ph.connStart)
		time.Sleep(time.Millisecond)
		ph.markDone(&ph.connStart, &ph.connectUs)
	}()
	go func() {
		defer wg.Done()
		ph.markFirstByte()
	}()
	wg.Wait()

	dnsUs, connectUs, _, fb := ph.snapshot()
	if dnsUs <= 0 {
		t.Errorf("dnsUs = %d, want > 0", dnsUs)
	}
	if connectUs <= 0 {
		t.Errorf("connectUs = %d, want > 0", connectUs)
	}
	if fb.IsZero() {
		t.Error("firstByte not recorded")
	}
}

// TestPhaseMarkDoneWithoutStart ensures a done callback that fires without a
// matching start (e.g. a connection reused from the pool) records nothing
// rather than a bogus huge duration.
func TestPhaseMarkDoneWithoutStart(t *testing.T) {
	ph := &phase{}
	ph.markDone(&ph.tlsStart, &ph.tlsUs)
	if _, _, tlsUs, _ := ph.snapshot(); tlsUs != 0 {
		t.Errorf("tlsUs = %d, want 0 when start was never set", tlsUs)
	}
}

// TestPhaseFirstByteRecordedOnce verifies the first-byte timestamp is latched
// once (GotFirstResponseByte can fire more than once on some transports).
func TestPhaseFirstByteRecordedOnce(t *testing.T) {
	ph := &phase{}
	ph.markFirstByte()
	_, _, _, first := ph.snapshot()
	time.Sleep(2 * time.Millisecond)
	ph.markFirstByte()
	_, _, _, second := ph.snapshot()
	if !first.Equal(second) {
		t.Errorf("firstByte changed on second mark: %v -> %v", first, second)
	}
}

func BenchmarkPhaseTiming(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ph := &phase{}
		ph.markStart(&ph.dnsStart)
		ph.markDone(&ph.dnsStart, &ph.dnsUs)
		ph.markStart(&ph.connStart)
		ph.markDone(&ph.connStart, &ph.connectUs)
		ph.markStart(&ph.tlsStart)
		ph.markDone(&ph.tlsStart, &ph.tlsUs)
		ph.markFirstByte()
		ph.snapshot()
	}
}
