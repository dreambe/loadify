// Package sysstat samples the worker process's CPU and memory usage so the
// coordinator (and the UI) can show per-node load. It reads /proc on Linux and
// degrades to zero CPU elsewhere.
package sysstat

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// clkTck is the assumed clock-ticks-per-second (Linux default; getconf CLK_TCK).
const clkTck = 100.0

// CPUSampler computes process CPU percentage between successive Sample calls.
// It is not safe for concurrent use.
type CPUSampler struct {
	lastTicks float64
	lastTime  time.Time
}

// NewCPUSampler returns a sampler primed with the current counters.
func NewCPUSampler() *CPUSampler {
	s := &CPUSampler{}
	s.lastTicks = procTicks()
	s.lastTime = time.Now()
	return s
}

// Sample returns CPU usage since the previous call, as a percentage of one core
// (so a fully-busy process on a multi-core box can exceed 100). Returns 0 when
// process stats are unavailable (non-Linux) or on the first interval.
func (s *CPUSampler) Sample() float64 {
	now := time.Now()
	ticks := procTicks()
	dt := now.Sub(s.lastTime).Seconds()
	dticks := ticks - s.lastTicks
	s.lastTicks = ticks
	s.lastTime = now
	if dt <= 0 || dticks < 0 || ticks == 0 {
		return 0
	}
	return (dticks / clkTck) / dt * 100.0
}

// MemBytes returns the process's memory footprint from the runtime.
func MemBytes() int64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.Sys)
}

// procTicks reads utime+stime (in clock ticks) from /proc/self/stat, or 0.
func procTicks() float64 {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	// The comm field (2) is parenthesized and may contain spaces; parse after
	// the last ')'. utime/stime are then tokens index 11 and 12.
	s := string(data)
	rp := strings.LastIndexByte(s, ')')
	if rp < 0 || rp+2 >= len(s) {
		return 0
	}
	fields := strings.Fields(s[rp+2:])
	if len(fields) < 13 {
		return 0
	}
	utime, err1 := strconv.ParseFloat(fields[11], 64)
	stime, err2 := strconv.ParseFloat(fields[12], 64)
	if err1 != nil || err2 != nil {
		return 0
	}
	return utime + stime
}
