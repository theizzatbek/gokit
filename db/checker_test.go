package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

func TestNewChecker_DefaultName(t *testing.T) {
	c := db.NewChecker(nil, "")
	if got := c.Name(); got != "db" {
		t.Errorf("default name = %q, want db", got)
	}
}

func TestNewChecker_CustomName(t *testing.T) {
	c := db.NewChecker(nil, "primary")
	if got := c.Name(); got != "primary" {
		t.Errorf("name = %q, want primary", got)
	}
}

func TestChecker_Nil_ReturnsUnavailable(t *testing.T) {
	c := db.NewChecker(nil, "")
	err := c.Check(context.Background())
	if err == nil {
		t.Fatal("Check on nil pool should fail")
	}
	var ee *errs.Error
	if !errors.As(err, &ee) {
		t.Fatalf("err is not *errs.Error: %T", err)
	}
	if ee.Kind != errs.KindUnavailable {
		t.Errorf("kind = %v, want Unavailable", ee.Kind)
	}
}
