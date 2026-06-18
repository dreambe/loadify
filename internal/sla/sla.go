// Package sla evaluates pass/fail thresholds against a run's summary metrics,
// modelled after k6 thresholds (e.g. p95 < 200ms, error_rate < 1%). Any failed
// threshold fails the whole run.
package sla

import "fmt"

// Metric names a comparable summary value.
const (
	MetricP50       = "p50_ms"
	MetricP90       = "p90_ms"
	MetricP95       = "p95_ms"
	MetricP99       = "p99_ms"
	MetricErrorRate = "error_rate" // percent (0-100)
	MetricQPS       = "qps"
)

// Threshold is one pass criterion: metric OP value (e.g. p95_ms < 200).
type Threshold struct {
	Metric string  `json:"metric"`
	Op     string  `json:"op"` // < <= > >=
	Value  float64 `json:"value"`
}

// Metrics holds the run summary values thresholds are evaluated against.
type Metrics struct {
	P50ms     float64
	P90ms     float64
	P95ms     float64
	P99ms     float64
	ErrorRate float64 // percent (0-100)
	QPS       float64
}

// Check is the result of evaluating one threshold.
type Check struct {
	Metric string  `json:"metric"`
	Op     string  `json:"op"`
	Value  float64 `json:"value"`
	Actual float64 `json:"actual"`
	OK     bool    `json:"ok"`
}

// Evaluate checks every threshold against m. passed is true only if all pass.
// With no thresholds it returns passed=true and no checks.
func Evaluate(ths []Threshold, m Metrics) (bool, []Check) {
	passed := true
	checks := make([]Check, 0, len(ths))
	for _, th := range ths {
		actual := value(th.Metric, m)
		ok := compare(actual, th.Op, th.Value)
		if !ok {
			passed = false
		}
		checks = append(checks, Check{Metric: th.Metric, Op: th.Op, Value: th.Value, Actual: actual, OK: ok})
	}
	return passed, checks
}

func value(metric string, m Metrics) float64 {
	switch metric {
	case MetricP50:
		return m.P50ms
	case MetricP90:
		return m.P90ms
	case MetricP95:
		return m.P95ms
	case MetricP99:
		return m.P99ms
	case MetricErrorRate:
		return m.ErrorRate
	case MetricQPS:
		return m.QPS
	default:
		return 0
	}
}

func compare(a float64, op string, b float64) bool {
	switch op {
	case "<":
		return a < b
	case "<=":
		return a <= b
	case ">":
		return a > b
	case ">=":
		return a >= b
	default:
		return false
	}
}

// Valid reports whether a threshold references a known metric and operator.
func (t Threshold) Valid() error {
	switch t.Metric {
	case MetricP50, MetricP90, MetricP95, MetricP99, MetricErrorRate, MetricQPS:
	default:
		return fmt.Errorf("sla: unknown metric %q", t.Metric)
	}
	switch t.Op {
	case "<", "<=", ">", ">=":
	default:
		return fmt.Errorf("sla: unknown operator %q", t.Op)
	}
	return nil
}
