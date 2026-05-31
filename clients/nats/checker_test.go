package natsclient_test

import (
	"context"
	"errors"
	"testing"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/errs"
)

func TestNewChecker_DefaultName(t *testing.T) {
	c := natsclient.NewChecker(nil, "")
	if got := c.Name(); got != "nats" {
		t.Errorf("default name = %q, want nats", got)
	}
}

func TestNewChecker_CustomName(t *testing.T) {
	c := natsclient.NewChecker(nil, "events")
	if got := c.Name(); got != "events" {
		t.Errorf("name = %q, want events", got)
	}
}

func TestChecker_NilClient_ReturnsUnavailable(t *testing.T) {
	c := natsclient.NewChecker(nil, "")
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
	if ee.Code != natsclient.CodeNotReady {
		t.Errorf("code = %v, want %v", ee.Code, natsclient.CodeNotReady)
	}
}
