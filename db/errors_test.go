package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/theizzatbek/fibermap/errs"
)

func TestMapPgxErr_NilStaysNil(t *testing.T) {
	if mapPgxErr(nil) != nil {
		t.Fatal("expected nil")
	}
}

func TestMapPgxErr_PgxNoRows_KindNotFound(t *testing.T) {
	got := mapPgxErr(pgx.ErrNoRows)
	assertKindCode(t, got, errs.KindNotFound, "not_found")
	if !errors.Is(got, pgx.ErrNoRows) {
		t.Fatal("errors.Is(pgx.ErrNoRows) should still hold after wrap")
	}
}

func TestMapPgxErr_ContextDeadline_KindTimeout(t *testing.T) {
	assertKindCode(t, mapPgxErr(context.DeadlineExceeded), errs.KindTimeout, "db_timeout")
	assertKindCode(t, mapPgxErr(context.Canceled), errs.KindTimeout, "db_timeout")
}

func TestMapPgxErr_SqlStateBranches(t *testing.T) {
	cases := []struct {
		state    string
		wantKind errs.Kind
		wantCode string
	}{
		{"23505", errs.KindAlreadyExists, "already_exists"},
		{"23503", errs.KindConflict, "fk_violation"},
		{"40001", errs.KindConflict, "tx_conflict"},
		{"40P01", errs.KindConflict, "tx_conflict"},
		{"57014", errs.KindTimeout, "db_timeout"},
		{"08000", errs.KindUnavailable, "db_unavailable"},
		{"08006", errs.KindUnavailable, "db_unavailable"},
	}
	for _, c := range cases {
		t.Run(c.state, func(t *testing.T) {
			got := mapPgxErr(&pgconn.PgError{Code: c.state, Message: "x"})
			assertKindCode(t, got, c.wantKind, c.wantCode)
		})
	}
}

func TestMapPgxErr_UnmappedSqlState_KindInternal(t *testing.T) {
	got := mapPgxErr(&pgconn.PgError{Code: "23502", Message: "null"})
	assertKindCode(t, got, errs.KindInternal, "db_failure")
}

func TestMapPgxErr_UnknownError_KindInternal(t *testing.T) {
	assertKindCode(t, mapPgxErr(errors.New("boom")), errs.KindInternal, "db_failure")
}

func assertKindCode(t *testing.T, err error, wantKind errs.Kind, wantCode string) {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *errs.Error, got %T (%v)", err, err)
	}
	if e.Kind != wantKind {
		t.Fatalf("Kind = %v, want %v", e.Kind, wantKind)
	}
	if e.Code != wantCode {
		t.Fatalf("Code = %q, want %q", e.Code, wantCode)
	}
}
