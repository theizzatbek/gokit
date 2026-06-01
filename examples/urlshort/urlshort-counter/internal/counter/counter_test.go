package counter

import (
	"strings"
	"testing"
	"time"
)

// TestBuildVisitUpdate_ProducesUnnestSQL — sanity that the query
// builder emits the unnest-based UPDATE the production handler
// expects. Doesn't hit the DB; just renders SQL + args to confirm
// shape stability under reorder / refactor.
func TestBuildVisitUpdate_ProducesUnnestSQL(t *testing.T) {
	agg := map[string]visitAgg{
		"abc": {delta: 3, lastTS: time.Now()},
		"def": {delta: 1, lastTS: time.Now()},
	}
	q := buildVisitUpdate(agg)
	sql, args, err := q.ToSql()
	if err != nil {
		t.Fatalf("ToSql: %v", err)
	}
	if !strings.Contains(sql, "unnest(") {
		t.Errorf("SQL missing unnest: %s", sql)
	}
	if !strings.Contains(sql, "FROM unnest") {
		t.Errorf("SQL missing FROM unnest clause: %s", sql)
	}
	if len(args) != 3 {
		t.Fatalf("args = %d, want 3 (codes, deltas, timestamps)", len(args))
	}
	// codes is the first arg.
	codes, ok := args[0].([]string)
	if !ok {
		t.Fatalf("args[0] type = %T, want []string", args[0])
	}
	if len(codes) != 2 {
		t.Errorf("codes len = %d, want 2", len(codes))
	}
}

func TestBuildVisitUpdate_EmptyAggStillCompiles(t *testing.T) {
	q := buildVisitUpdate(map[string]visitAgg{})
	if _, _, err := q.ToSql(); err != nil {
		t.Errorf("ToSql on empty agg: %v", err)
	}
}
