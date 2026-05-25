package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/errs"
)

func TestIntegration_UniqueViolation_KindAlreadyExists(t *testing.T) {
	d := startTestDB(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `CREATE TABLE t (id int PRIMARY KEY)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO t VALUES (1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	_, err := d.Exec(ctx, `INSERT INTO t VALUES (1)`)
	assertKindCodeDB(t, err, errs.KindAlreadyExists, "already_exists")
}

func TestIntegration_FKViolation_KindConflict(t *testing.T) {
	d := startTestDB(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `CREATE TABLE parent (id int PRIMARY KEY)`); err != nil {
		t.Fatalf("parent: %v", err)
	}
	if _, err := d.Exec(ctx, `CREATE TABLE child (pid int REFERENCES parent(id))`); err != nil {
		t.Fatalf("child: %v", err)
	}
	_, err := d.Exec(ctx, `INSERT INTO child VALUES (42)`)
	assertKindCodeDB(t, err, errs.KindConflict, "fk_violation")
}

func TestIntegration_ContextTimeout_KindTimeout(t *testing.T) {
	d := startTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := d.Exec(ctx, `SELECT pg_sleep(2)`)
	assertKindCodeDB(t, err, errs.KindTimeout, "db_timeout")
}

func assertKindCodeDB(t *testing.T, err error, wantKind errs.Kind, wantCode string) {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *errs.Error, got %T (%v)", err, err)
	}
	if e.Kind != wantKind || e.Code != wantCode {
		t.Fatalf("got Kind=%v Code=%q, want %v %q", e.Kind, e.Code, wantKind, wantCode)
	}
}
