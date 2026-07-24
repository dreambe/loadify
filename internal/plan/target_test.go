package plan

import "testing"

func TestTargetMonitorEffectiveSelector(t *testing.T) {
	cases := []struct {
		name string
		m    *TargetMonitor
		want string
	}{
		{"nil", nil, ""},
		{"empty", &TargetMonitor{Enabled: true}, ""},
		{"label+value", &TargetMonitor{Enabled: true, Label: "job", Value: "prism-api"}, `job="prism-api"`},
		{"default label is job", &TargetMonitor{Enabled: true, Value: "checkout"}, `job="checkout"`},
		{"other label", &TargetMonitor{Enabled: true, Label: "app", Value: "cart"}, `app="cart"`},
		{"custom selector wins", &TargetMonitor{Enabled: true, Label: "job", Value: "x", Selector: `instance=~"web-.*"`}, `instance=~"web-.*"`},
		{"legacy instance fallback", &TargetMonitor{Enabled: true, Instance: "10.0.0.5:9100"}, `instance="10.0.0.5:9100"`},
		{"value is escaped", &TargetMonitor{Enabled: true, Label: "job", Value: `a"b`}, `job="a\"b"`},
		{"label is sanitized", &TargetMonitor{Enabled: true, Label: "job;drop", Value: "x"}, `jobdrop="x"`},
	}
	for _, c := range cases {
		if got := c.m.EffectiveSelector(); got != c.want {
			t.Errorf("%s: EffectiveSelector() = %q, want %q", c.name, got, c.want)
		}
	}
}
