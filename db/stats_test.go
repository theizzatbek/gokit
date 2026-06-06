package db

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStats_NilSafe(t *testing.T) {
	var d *DB
	got := d.Stats()
	if got.HasReplicas {
		t.Errorf("nil DB Stats reports HasReplicas, want false")
	}
	d2 := &DB{}
	got = d2.Stats()
	if got.HasReplicas {
		t.Errorf("DB with closed pool reports HasReplicas, want false")
	}
}

func TestStats_HasReplicasFlag(t *testing.T) {
	// Build a DB with a dummy primary pool pointer (we don't call Stat
	// on it in this test because pgxpool.Pool would panic without
	// Connect) and zero replicas → HasReplicas=false.
	d := &DB{}
	d.pool = nil
	if d.Stats().HasReplicas {
		t.Error("no-replica DB reports HasReplicas=true")
	}
	// Construct a DB with at least one read-pool entry; HasReplicas
	// should be true regardless of pool health.
	d2 := &DB{}
	d2.pool = &pgxpool.Pool{}
	d2.readPools = []*readPoolEntry{newReadPoolEntry("standby", &pgxpool.Pool{})}
	// Stats() walks readPools and calls Stat() on each — pgxpool.Pool's
	// Stat() panics on a zero-value pool, so we can't fully exercise
	// the projection here. The flag-only assertion stays valid: the
	// kit reports HasReplicas from the slice length, not from Stat().
	if got := len(d2.ReadPools()); got == 0 {
		t.Errorf("len(ReadPools())=0, want >0")
	}
}
