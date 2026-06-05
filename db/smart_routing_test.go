package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// counterValueByLabels gathers a counter labelled by the supplied map.
func counterValueByLabels(t *testing.T, reg *prometheus.Registry, name string, want map[string]string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.Metric {
			if labelsMatch(m.GetLabel(), want) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func labelsMatch(got []*dto.LabelPair, want map[string]string) bool {
	have := map[string]string{}
	for _, l := range got {
		have[l.GetName()] = l.GetValue()
	}
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

func TestEligibleReadPools_AllHealthyNoBudget(t *testing.T) {
	d := &DB{
		readPools: []*readPoolEntry{
			newReadPoolEntry("a", &pgxpool.Pool{}),
			newReadPoolEntry("b", &pgxpool.Pool{}),
		},
	}
	got := d.eligibleReadPools()
	if len(got) != 2 {
		t.Errorf("got %d eligible, want 2", len(got))
	}
}

func TestEligibleReadPools_UnhealthySkipped(t *testing.T) {
	reg := prometheus.NewRegistry()
	d := &DB{
		readPools: []*readPoolEntry{
			newReadPoolEntry("a", &pgxpool.Pool{}),
			newReadPoolEntry("b", &pgxpool.Pool{}),
		},
		opts: options{metrics: newMetricsCollector(reg)},
	}
	d.readPools[1].healthy.Store(false)

	got := d.eligibleReadPools()
	if len(got) != 1 || got[0].name != "a" {
		t.Errorf("got %d eligible (names=%v), want [a]", len(got), poolNames(got))
	}

	skipped := counterValueByLabels(t, reg, "db_replica_skipped_total",
		map[string]string{"pool": "b", "reason": "unhealthy"})
	if skipped != 1 {
		t.Errorf("skipped[b,unhealthy] = %v, want 1", skipped)
	}
}

func TestEligibleReadPools_OverBudget(t *testing.T) {
	reg := prometheus.NewRegistry()
	d := &DB{
		readPools: []*readPoolEntry{
			newReadPoolEntry("a", &pgxpool.Pool{}),
			newReadPoolEntry("b", &pgxpool.Pool{}),
		},
		opts: options{
			metrics:       newMetricsCollector(reg),
			readLagBudget: 500 * time.Millisecond,
		},
	}
	d.readPools[0].lagMillis.Store(100) // within budget
	d.readPools[1].lagMillis.Store(900) // over budget

	got := d.eligibleReadPools()
	if len(got) != 1 || got[0].name != "a" {
		t.Errorf("got %v, want [a]", poolNames(got))
	}
	skipped := counterValueByLabels(t, reg, "db_replica_skipped_total",
		map[string]string{"pool": "b", "reason": "over_budget"})
	if skipped != 1 {
		t.Errorf("skipped[b,over_budget] = %v, want 1", skipped)
	}
}

func TestEligibleReadPools_NoProbeYetTreatedAsInBudget(t *testing.T) {
	d := &DB{
		readPools: []*readPoolEntry{
			newReadPoolEntry("a", &pgxpool.Pool{}),
		},
		opts: options{readLagBudget: 100 * time.Millisecond},
	}
	// lagMillis starts at -1 ("no probe yet"); should NOT be skipped
	// even though -1 < budget would naively suggest "over budget".
	got := d.eligibleReadPools()
	if len(got) != 1 {
		t.Errorf("got %d, want 1 (no-probe replica must be eligible)", len(got))
	}
}

func TestPickReadPool_AllReplicasFilteredFallsBackToPrimary(t *testing.T) {
	primary := &pgxpool.Pool{}
	reg := prometheus.NewRegistry()
	d := &DB{
		pool: primary,
		readPools: []*readPoolEntry{
			newReadPoolEntry("a", &pgxpool.Pool{}),
		},
		opts: options{metrics: newMetricsCollector(reg)},
	}
	d.readPools[0].healthy.Store(false)

	got := d.pickReadPool(context.Background())
	if got != primary {
		t.Errorf("expected fallback to primary, got %p", got)
	}
	mfs, _ := reg.Gather()
	var fallback float64
	for _, mf := range mfs {
		if mf.GetName() == "db_replica_fallback_total" {
			fallback = mf.Metric[0].GetCounter().GetValue()
		}
	}
	if fallback != 1 {
		t.Errorf("db_replica_fallback_total = %v, want 1", fallback)
	}
}

// Helper to print pool names from a slice for diagnostic Error messages.
func poolNames(in []*readPoolEntry) []string {
	out := make([]string, 0, len(in))
	for _, e := range in {
		out = append(out, e.name)
	}
	return out
}
