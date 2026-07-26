package svckit

import (
	"context"
	"testing"

	"github.com/theizzatbek/gokit/fibermap"
)

// checkerMod adds a readiness checker during the Build phase.
type checkerMod struct{ name string }

func (m checkerMod) Name() string { return m.name }

func (m checkerMod) Build(_ context.Context, h Host) error {
	h.AddReadinessChecker(stubChecker{name: m.name})
	return nil
}

type stubChecker struct{ name string }

func (c stubChecker) Name() string                  { return c.name }
func (c stubChecker) Check(_ context.Context) error { return nil }

func TestReadinessCheckers_IncludeModContributions(t *testing.T) {
	svc, err := New[struct{}, struct{}](context.Background(), Config{},
		WithMod(checkerMod{name: "alpha"}),
		WithMod(checkerMod{name: "beta"}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	checkers := svc.readinessCheckers()

	if len(checkers) != 2 {
		t.Fatalf("want 2 checkers, got %d", len(checkers))
	}
	// order of registration is preserved — preflight JSON reads
	// top-to-bottom as a dependency tree
	if checkers[0].Name() != "alpha" || checkers[1].Name() != "beta" {
		t.Errorf("order not preserved: %q, %q", checkers[0].Name(), checkers[1].Name())
	}
	var _ []fibermap.Checker = checkers
}
