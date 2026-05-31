package redisclient_test

import (
	"context"
	"errors"
	"testing"

	redisclient "github.com/theizzatbek/gokit/clients/redis"
	"github.com/theizzatbek/gokit/errs"
)

func TestNewChecker_DefaultName(t *testing.T) {
	c := redisclient.NewChecker(nil, "")
	if got := c.Name(); got != "redis" {
		t.Errorf("default name = %q, want redis", got)
	}
}

func TestNewChecker_CustomName(t *testing.T) {
	c := redisclient.NewChecker(nil, "cache")
	if got := c.Name(); got != "cache" {
		t.Errorf("name = %q, want cache", got)
	}
}

func TestChecker_NilClient_ReturnsUnavailable(t *testing.T) {
	c := redisclient.NewChecker(nil, "")
	err := c.Check(context.Background())
	if err == nil {
		t.Fatal("Check on nil client should fail")
	}
	var ee *errs.Error
	if !errors.As(err, &ee) {
		t.Fatalf("err is not *errs.Error: %T", err)
	}
	if ee.Kind != errs.KindUnavailable {
		t.Errorf("kind = %v, want Unavailable", ee.Kind)
	}
	if ee.Code != redisclient.CodeNotReady {
		t.Errorf("code = %v, want %v", ee.Code, redisclient.CodeNotReady)
	}
}
