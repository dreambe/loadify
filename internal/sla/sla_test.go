package sla_test

import (
	"testing"

	"github.com/dreambe/loadify/internal/sla"
)

func TestEvaluate(t *testing.T) {
	m := sla.Metrics{P95ms: 180, ErrorRate: 0.5, QPS: 1200}

	passed, checks := sla.Evaluate([]sla.Threshold{
		{Metric: sla.MetricP95, Op: "<", Value: 200},
		{Metric: sla.MetricErrorRate, Op: "<", Value: 1},
		{Metric: sla.MetricQPS, Op: ">=", Value: 1000},
	}, m)
	if !passed {
		t.Fatalf("expected pass, checks=%+v", checks)
	}
	if len(checks) != 3 {
		t.Fatalf("checks = %d, want 3", len(checks))
	}

	passed, checks = sla.Evaluate([]sla.Threshold{
		{Metric: sla.MetricP95, Op: "<", Value: 100}, // 180 !< 100 -> fail
	}, m)
	if passed {
		t.Error("expected fail when p95 breaches")
	}
	if checks[0].OK || checks[0].Actual != 180 {
		t.Errorf("unexpected check: %+v", checks[0])
	}
}

func TestEvaluateNoThresholds(t *testing.T) {
	passed, checks := sla.Evaluate(nil, sla.Metrics{})
	if !passed || len(checks) != 0 {
		t.Errorf("no thresholds should pass with no checks; got passed=%v checks=%d", passed, len(checks))
	}
}

func TestThresholdValid(t *testing.T) {
	if err := (sla.Threshold{Metric: sla.MetricP99, Op: "<="}).Valid(); err != nil {
		t.Errorf("expected valid: %v", err)
	}
	if err := (sla.Threshold{Metric: "bogus", Op: "<"}).Valid(); err == nil {
		t.Error("expected invalid metric")
	}
	if err := (sla.Threshold{Metric: sla.MetricP50, Op: "=="}).Valid(); err == nil {
		t.Error("expected invalid operator")
	}
}
