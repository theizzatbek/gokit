package service

import "testing"

func TestWithoutAutoDBMetrics_SetsFlag(t *testing.T) {
	o := options{}
	WithoutAutoDBMetrics()(&o)
	if !o.skipAutoDBMetrics {
		t.Error("WithoutAutoDBMetrics did not set skipAutoDBMetrics")
	}
}

func TestSkipAutoDBMetrics_DefaultsFalse(t *testing.T) {
	o := options{}
	if o.skipAutoDBMetrics {
		t.Error("default options.skipAutoDBMetrics = true, want false (auto-wire is opt-in by default)")
	}
}
