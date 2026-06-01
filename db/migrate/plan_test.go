package migrate_test

import (
	"context"
	"testing"

	"github.com/theizzatbek/gokit/db/migrate"
	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestPlan_EmptyOnUpToDateDB(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	fs := memFS(map[string]string{
		"0001_init.sql": `CREATE TABLE widgets (id int PRIMARY KEY)`,
	})
	if err := migrate.Up(ctx, d, fs); err != nil {
		t.Fatalf("Up: %v", err)
	}
	pending, err := migrate.Plan(ctx, d, fs)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %d, want 0 on current DB", len(pending))
	}
}

func TestPlan_ListsOnlyPending(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	fsBefore := memFS(map[string]string{
		"0001_init.sql": `CREATE TABLE widgets (id int PRIMARY KEY)`,
	})
	if err := migrate.Up(ctx, d, fsBefore); err != nil {
		t.Fatalf("Up: %v", err)
	}
	fsAfter := memFS(map[string]string{
		"0001_init.sql":     `CREATE TABLE widgets (id int PRIMARY KEY)`,
		"0002_add_name.sql": `ALTER TABLE widgets ADD COLUMN name text`,
		"0003_add_qty.sql":  `ALTER TABLE widgets ADD COLUMN qty int`,
	})
	pending, err := migrate.Plan(ctx, d, fsAfter)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending = %d, want 2", len(pending))
	}
	if pending[0].Version != "0002" || pending[1].Version != "0003" {
		t.Errorf("pending versions = %v, want 0002 then 0003",
			[]string{pending[0].Version, pending[1].Version})
	}
}

func TestUpTo_AppliesUpToTargetOnly(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	fs := memFS(map[string]string{
		"0001_init.sql":     `CREATE TABLE widgets (id int PRIMARY KEY)`,
		"0002_add_name.sql": `ALTER TABLE widgets ADD COLUMN name text`,
		"0003_add_qty.sql":  `ALTER TABLE widgets ADD COLUMN qty int`,
	})
	if err := migrate.UpTo(ctx, d, fs, "0002"); err != nil {
		t.Fatalf("UpTo: %v", err)
	}
	v, _ := migrate.Version(ctx, d)
	if v != "0002" {
		t.Errorf("Version = %q, want 0002 after UpTo(0002)", v)
	}
}

func TestUpTo_UnknownTargetReturnsErr(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	fs := memFS(map[string]string{
		"0001_init.sql": `CREATE TABLE widgets (id int PRIMARY KEY)`,
	})
	err := migrate.UpTo(ctx, d, fs, "9999")
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
	if e, ok := err.(*xerrs.Error); !ok || e.Code != migrate.CodeUnknownTarget {
		t.Errorf("err = %v, want CodeUnknownTarget", err)
	}
}

func TestDownTo_RollsBackUntilTarget(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	fs := memFS(map[string]string{
		"0001_init.sql":          `CREATE TABLE widgets (id int PRIMARY KEY)`,
		"0001_init.down.sql":     `DROP TABLE widgets`,
		"0002_add_name.sql":      `ALTER TABLE widgets ADD COLUMN name text`,
		"0002_add_name.down.sql": `ALTER TABLE widgets DROP COLUMN name`,
		"0003_add_qty.sql":       `ALTER TABLE widgets ADD COLUMN qty int`,
		"0003_add_qty.down.sql":  `ALTER TABLE widgets DROP COLUMN qty`,
	})
	if err := migrate.Up(ctx, d, fs); err != nil {
		t.Fatalf("Up: %v", err)
	}
	// DownTo(0001) → 0002 + 0003 rolled back; 0001 stays.
	if err := migrate.DownTo(ctx, d, fs, "0001"); err != nil {
		t.Fatalf("DownTo: %v", err)
	}
	v, _ := migrate.Version(ctx, d)
	if v != "0001" {
		t.Errorf("Version = %q, want 0001 after DownTo(0001)", v)
	}
}

func TestDownTo_OnTargetVersionIsNoOp(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	fs := memFS(map[string]string{
		"0001_init.sql":      `CREATE TABLE widgets (id int PRIMARY KEY)`,
		"0001_init.down.sql": `DROP TABLE widgets`,
	})
	if err := migrate.Up(ctx, d, fs); err != nil {
		t.Fatalf("Up: %v", err)
	}
	// DownTo the current target → nothing to roll back.
	if err := migrate.DownTo(ctx, d, fs, "0001"); err != nil {
		t.Fatalf("DownTo: %v", err)
	}
	v, _ := migrate.Version(ctx, d)
	if v != "0001" {
		t.Errorf("Version = %q, want 0001 (no-op)", v)
	}
}
