package testdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/testdb"
)

// TestSpin_RoundTrip verifies the single-node helper hands back a
// usable *db.DB whose default per-test schema isolates concurrent
// callers. Skips under -short.
func TestSpin_RoundTrip(t *testing.T) {
	d := testdb.Spin(t)
	ctx := context.Background()

	if _, err := d.Exec(ctx, `CREATE TABLE t (n int)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO t (n) VALUES (1), (2), (3)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var n int
	if err := d.QueryRow(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("count = %d, want 3", n)
	}
}

// TestSpin_SchemaIsolation verifies two concurrent Spin calls in the
// same binary get different schemas — a table created by one is
// invisible to the other. Catches regressions in spinShared.
func TestSpin_SchemaIsolation(t *testing.T) {
	d1 := testdb.Spin(t)
	d2 := testdb.Spin(t)
	ctx := context.Background()

	if _, err := d1.Exec(ctx, `CREATE TABLE only_in_d1 (n int)`); err != nil {
		t.Fatalf("create on d1: %v", err)
	}
	// d2's search_path points at a different schema → table is not
	// visible.
	if _, err := d2.Exec(ctx, `SELECT * FROM only_in_d1`); err == nil {
		t.Error("d2 saw d1's table — schema isolation broken")
	}
}

// TestSpinCluster_ReplicationConsistency is the smoke test for the
// cluster helper: write on Primary → WaitForReplication → SELECT
// through Multi must observe the row.
//
// Slow (boots 1 primary + 1 replica via bitnami). Suppressed under
// -short.
func TestSpinCluster_ReplicationConsistency(t *testing.T) {
	c := testdb.SpinCluster(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := c.Primary.Exec(ctx, `CREATE TABLE links (id int PRIMARY KEY)`); err != nil {
		t.Fatalf("create on primary: %v", err)
	}
	if _, err := c.Primary.Exec(ctx, `INSERT INTO links (id) VALUES (1), (2)`); err != nil {
		t.Fatalf("insert on primary: %v", err)
	}

	if err := c.WaitForReplication(ctx); err != nil {
		t.Fatalf("wait: %v", err)
	}

	// SELECT through Multi — routes to the replica.
	var n int
	if err := c.Multi.ReadQueryRow(ctx, `SELECT count(*) FROM links`).Scan(&n); err != nil {
		t.Fatalf("count via Multi: %v", err)
	}
	if n != 2 {
		t.Errorf("Multi.ReadQueryRow count = %d, want 2", n)
	}

	// SELECT directly on Replicas[0] — also sees the row.
	if err := c.Replicas[0].QueryRow(ctx, `SELECT count(*) FROM links`).Scan(&n); err != nil {
		t.Fatalf("count via Replicas[0]: %v", err)
	}
	if n != 2 {
		t.Errorf("Replicas[0] count = %d, want 2", n)
	}
}

// TestSpinCluster_ReadFromPrimary verifies the ReadFromPrimary
// ctx-marker routes around the replica even on the Multi handle —
// useful immediately after a write tx.
func TestSpinCluster_ReadFromPrimary(t *testing.T) {
	c := testdb.SpinCluster(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := c.Primary.Exec(ctx, `CREATE TABLE counters (v int)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := c.Primary.Exec(ctx, `INSERT INTO counters (v) VALUES (42)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Without WaitForReplication: the row may not yet have replicated.
	// db.ReadFromPrimary forces the read onto the writable pool.
	var v int
	if err := c.Multi.ReadQueryRow(db.ReadFromPrimary(ctx),
		`SELECT v FROM counters LIMIT 1`).Scan(&v); err != nil {
		t.Fatalf("ReadFromPrimary read: %v", err)
	}
	if v != 42 {
		t.Errorf("v = %d, want 42 (ReadFromPrimary should hit the writable pool)", v)
	}
}

// TestSpinCluster_ReplicationLagMetric verifies the kit's
// ReplicationLag helper returns sensible numbers against the live
// cluster.
func TestSpinCluster_ReplicationLagMetric(t *testing.T) {
	c := testdb.SpinCluster(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := c.WaitForReplication(ctx); err != nil {
		t.Fatalf("warmup wait: %v", err)
	}

	infos := c.Multi.ReplicationLag(ctx)
	if len(infos) != 2 {
		t.Fatalf("ReplicationLag returned %d entries, want 2", len(infos))
	}
	for _, info := range infos {
		if !info.Healthy {
			t.Errorf("pool %s unhealthy: %v", info.PoolName, info.Err)
		}
		if info.LagSeconds < 0 {
			t.Errorf("pool %s lag = %f, want >= 0", info.PoolName, info.LagSeconds)
		}
	}
}
