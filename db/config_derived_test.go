package db

import (
	"testing"
	"time"
)

func TestConfigDerivedOptions_AllEmpty(t *testing.T) {
	if got := configDerivedOptions(Config{}); got != nil {
		t.Errorf("zero Config produced opts: %v, want nil", got)
	}
}

func TestConfigDerivedOptions_LagBudget(t *testing.T) {
	cfg := Config{LagBudget: 5 * time.Second}
	opts := configDerivedOptions(cfg)
	if len(opts) != 1 {
		t.Fatalf("len(opts) = %d, want 1", len(opts))
	}
	var o options
	opts[0](&o)
	if o.readLagBudget != 5*time.Second {
		t.Errorf("readLagBudget = %v, want 5s", o.readLagBudget)
	}
}

func TestConfigDerivedOptions_LagPolling(t *testing.T) {
	cfg := Config{
		LagPollInterval:  10 * time.Second,
		LagPollThreshold: 30 * time.Second,
	}
	opts := configDerivedOptions(cfg)
	if len(opts) != 1 {
		t.Fatalf("len(opts) = %d, want 1", len(opts))
	}
	var o options
	opts[0](&o)
	if o.lagPoll.interval != 10*time.Second {
		t.Errorf("interval = %v, want 10s", o.lagPoll.interval)
	}
	if o.lagPoll.threshold != 30*time.Second {
		t.Errorf("threshold = %v, want 30s", o.lagPoll.threshold)
	}
}

func TestConfigDerivedOptions_Both(t *testing.T) {
	cfg := Config{
		LagBudget:        2 * time.Second,
		LagPollInterval:  5 * time.Second,
		LagPollThreshold: 15 * time.Second,
	}
	opts := configDerivedOptions(cfg)
	if len(opts) != 2 {
		t.Errorf("len(opts) = %d, want 2 (polling + budget)", len(opts))
	}
}

func TestConfigDerivedOptions_ZeroIntervalSkipsPolling(t *testing.T) {
	// Threshold without interval is meaningless — we only emit the
	// polling option when interval > 0.
	cfg := Config{LagPollThreshold: 30 * time.Second}
	opts := configDerivedOptions(cfg)
	if len(opts) != 0 {
		t.Errorf("threshold-only Config produced %d opts, want 0", len(opts))
	}
}

// TestConfigDerivedOptions_OrderPolledFirst ensures the polling
// option lands before the budget option. The order matters because
// the kit's documented behaviour is "lag is tracked when polling
// runs; budget consults that tracked value" — if budget ran first
// against an unconfigured lagPoll, an operator reading the option
// list would be confused.
func TestConfigDerivedOptions_OrderPolledFirst(t *testing.T) {
	cfg := Config{
		LagBudget:       1 * time.Second,
		LagPollInterval: 1 * time.Second,
	}
	opts := configDerivedOptions(cfg)
	if len(opts) != 2 {
		t.Fatalf("len(opts) = %d, want 2", len(opts))
	}
	// Apply each in order; the FIRST should set lagPoll.interval.
	var o options
	opts[0](&o)
	if o.lagPoll.interval == 0 {
		t.Errorf("first opt was not the polling opt (interval still zero)")
	}
}
