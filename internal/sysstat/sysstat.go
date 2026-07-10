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

// MemHost returns host memory (used, total) in bytes from /proc/meminfo, so the
// UI can show a usage percentage. used = MemTotal - MemAvailable. Returns 0,0
// when /proc/meminfo is unavailable (non-Linux).
func MemHost() (used, total int64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	var totalKB, availKB int64
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseInt(f[1], 10, 64) // value is in kB
		switch f[0] {
		case "MemTotal:":
			totalKB = v
		case "MemAvailable:":
			availKB = v
		}
	}
	if totalKB == 0 {
		return 0, 0
	}
	used = (totalKB - availKB) * 1024
	if used < 0 {
		used = 0
	}
	return used, totalKB * 1024
}

// NetStats is a per-second network rate snapshot.
type NetStats struct {
	RxBps, TxBps int64 // bytes/sec
	RxPps, TxPps int64 // packets/sec
}

// NetSampler computes network throughput and packet rate between Sample calls
// from /proc/net/dev, summing all non-loopback interfaces. Not concurrent-safe.
type NetSampler struct {
	last     netCounters
	lastTime time.Time
	primed   bool
}

type netCounters struct{ rxBytes, txBytes, rxPkts, txPkts int64 }

// NewNetSampler returns a sampler primed with the current counters.
func NewNetSampler() *NetSampler {
	s := &NetSampler{}
	s.last = readNetCounters()
	s.lastTime = time.Now()
	s.primed = true
	return s
}

// Sample returns per-second network rates since the previous call (zero on the
// first interval or when /proc/net/dev is unavailable).
func (s *NetSampler) Sample() NetStats {
	now := time.Now()
	cur := readNetCounters()
	dt := now.Sub(s.lastTime).Seconds()
	prev := s.last
	s.last = cur
	s.lastTime = now
	if !s.primed || dt <= 0 {
		return NetStats{}
	}
	rate := func(a, b int64) int64 {
		if d := a - b; d > 0 {
			return int64(float64(d) / dt)
		}
		return 0
	}
	return NetStats{
		RxBps: rate(cur.rxBytes, prev.rxBytes),
		TxBps: rate(cur.txBytes, prev.txBytes),
		RxPps: rate(cur.rxPkts, prev.rxPkts),
		TxPps: rate(cur.txPkts, prev.txPkts),
	}
}

// readNetCounters sums rx/tx bytes and packets across non-loopback interfaces
// from /proc/net/dev. Fields after "iface:" are:
//
//	rxbytes rxpackets rxerrs rxdrop rxfifo rxframe rxcompressed rxmulticast
//	txbytes txpackets ...
func readNetCounters() netCounters {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return netCounters{}
	}
	var c netCounters
	for _, line := range strings.Split(string(data), "\n") {
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:colon])
		if iface == "" || iface == "lo" {
			continue
		}
		f := strings.Fields(line[colon+1:])
		if len(f) < 10 {
			continue
		}
		rxB, _ := strconv.ParseInt(f[0], 10, 64)
		rxP, _ := strconv.ParseInt(f[1], 10, 64)
		txB, _ := strconv.ParseInt(f[8], 10, 64)
		txP, _ := strconv.ParseInt(f[9], 10, 64)
		c.rxBytes += rxB
		c.rxPkts += rxP
		c.txBytes += txB
		c.txPkts += txP
	}
	return c
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
