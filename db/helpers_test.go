package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/theizzatbek/gokit/errs"
)

// stubQuerier is the minimal Querier impl we need for helpers_test.
// Each method records its inputs and returns canned data set on the
// struct fields.
type stubQuerier struct {
	gotSQL   string
	gotArgs  []any
	scanFunc func(dest ...any) error
	rowsFunc func() (pgx.Rows, error)
	execFunc func() (pgconn.CommandTag, error)
}

func (s *stubQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	s.gotSQL = sql
	s.gotArgs = args
	if s.rowsFunc != nil {
		return s.rowsFunc()
	}
	return nil, errors.New("Query stub not wired")
}

func (s *stubQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	s.gotSQL = sql
	s.gotArgs = args
	return rowStub{scan: s.scanFunc}
}

func (s *stubQuerier) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	s.gotSQL = sql
	s.gotArgs = args
	if s.execFunc != nil {
		return s.execFunc()
	}
	return pgconn.CommandTag{}, nil
}

type rowStub struct {
	scan func(dest ...any) error
}

func (r rowStub) Scan(dest ...any) error {
	if r.scan != nil {
		return r.scan(dest...)
	}
	return errors.New("scan stub not wired")
}

func TestExists_True(t *testing.T) {
	q := &stubQuerier{scanFunc: func(dest ...any) error {
		*dest[0].(*bool) = true
		return nil
	}}
	got, err := Exists(context.Background(), q, "SELECT 1 FROM users WHERE id = $1", "u-1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !got {
		t.Errorf("got false, want true")
	}
	want := "SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)"
	if q.gotSQL != want {
		t.Errorf("sql = %q, want %q", q.gotSQL, want)
	}
	if len(q.gotArgs) != 1 || q.gotArgs[0] != "u-1" {
		t.Errorf("args = %v", q.gotArgs)
	}
}

func TestExists_False(t *testing.T) {
	q := &stubQuerier{scanFunc: func(dest ...any) error {
		*dest[0].(*bool) = false
		return nil
	}}
	got, err := Exists(context.Background(), q, "SELECT 1 FROM users")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got {
		t.Errorf("got true, want false")
	}
}

func TestExists_NilQuerier(t *testing.T) {
	_, err := Exists(context.Background(), nil, "SELECT 1")
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		t.Errorf("err = %v, want KindValidation", err)
	}
}

func TestCount_WrapsInSubquery(t *testing.T) {
	q := &stubQuerier{scanFunc: func(dest ...any) error {
		*dest[0].(*int64) = 42
		return nil
	}}
	got, err := Count(context.Background(), q, "SELECT 1 FROM orders WHERE total > $1", 100)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
	want := "SELECT count(*) FROM (SELECT 1 FROM orders WHERE total > $1) AS _kit_count_subq"
	if q.gotSQL != want {
		t.Errorf("sql = %q, want %q", q.gotSQL, want)
	}
}

func TestNotFound_Classifier(t *testing.T) {
	if NotFound(nil) {
		t.Error("NotFound(nil) = true, want false")
	}
	if NotFound(errors.New("random")) {
		t.Error("NotFound(random) = true, want false")
	}
	if !NotFound(errs.NotFound("not_found", "x")) {
		t.Error("NotFound(KindNotFound) = false, want true")
	}
}

func TestWithQueryName_Roundtrip(t *testing.T) {
	if got := queryNameFrom(context.Background()); got != "" {
		t.Errorf("unmarked ctx name = %q, want empty", got)
	}
	ctx := WithQueryName(context.Background(), "user_lookup")
	if got := queryNameFrom(ctx); got != "user_lookup" {
		t.Errorf("name = %q, want user_lookup", got)
	}
	// Nested: inner wins.
	inner := WithQueryName(ctx, "session_load")
	if got := queryNameFrom(inner); got != "session_load" {
		t.Errorf("nested name = %q, want session_load (last write wins)", got)
	}
	// Outer untouched.
	if got := queryNameFrom(ctx); got != "user_lookup" {
		t.Errorf("outer ctx name leaked: %q, want user_lookup", got)
	}
}
