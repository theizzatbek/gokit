package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/fibermap"
)

// preflightFakeChecker is a controllable fibermap.Checker for unit tests.
type preflightFakeChecker struct {
	name  string
	err   error
	sleep time.Duration
}

func (f *preflightFakeChecker) Name() string { return f.name }
func (f *preflightFakeChecker) Check(ctx context.Context) error {
	if f.sleep > 0 {
		select {
		case <-time.After(f.sleep):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func TestPreflight_AllPassing(t *testing.T) {
	s := &Service[struct{}, struct{}]{
		opts: &options{
			readinessExtraCheckers: []fibermap.Checker{
				&preflightFakeChecker{name: "a"},
				&preflightFakeChecker{name: "b"},
			},
		},
	}
	res := s.PreflightResult(context.Background())
	if res.Status != "ok" {
		t.Errorf("Status = %q, want ok", res.Status)
	}
	if len(res.Checks) != 2 {
		t.Fatalf("len Checks = %d, want 2", len(res.Checks))
	}
	for _, c := range res.Checks {
		if c.Status != "ok" {
			t.Errorf("check %q status = %q, want ok", c.Name, c.Status)
		}
	}
	if err := s.Preflight(context.Background()); err != nil {
		t.Errorf("Preflight() = %v, want nil", err)
	}
}

func TestPreflight_OneFailure(t *testing.T) {
	s := &Service[struct{}, struct{}]{
		opts: &options{
			readinessExtraCheckers: []fibermap.Checker{
				&preflightFakeChecker{name: "a"},
				&preflightFakeChecker{name: "b", err: errors.New("broken")},
			},
		},
	}
	res := s.PreflightResult(context.Background())
	if res.Status != "fail" {
		t.Errorf("Status = %q, want fail", res.Status)
	}
	var found bool
	for _, c := range res.Checks {
		if c.Name == "b" && c.Status == "fail" && c.Error == "broken" {
			found = true
		}
	}
	if !found {
		t.Errorf("failed check not surfaced: %+v", res.Checks)
	}
	if err := s.Preflight(context.Background()); err == nil {
		t.Error("Preflight() = nil, want error")
	}
}

func TestPreflight_EmptyCheckersIsOK(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{}}
	res := s.PreflightResult(context.Background())
	if res.Status != "ok" {
		t.Errorf("Status = %q, want ok for no checkers", res.Status)
	}
}

func TestPreflight_TimeoutSurfacesAsFailure(t *testing.T) {
	s := &Service[struct{}, struct{}]{
		opts: &options{
			preflightTimeout: 50 * time.Millisecond,
			readinessExtraCheckers: []fibermap.Checker{
				&preflightFakeChecker{name: "slow", sleep: 500 * time.Millisecond},
			},
		},
	}
	res := s.PreflightResult(context.Background())
	if res.Status != "fail" {
		t.Errorf("Status = %q, want fail under timeout", res.Status)
	}
}
