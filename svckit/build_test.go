package svckit

import (
	"context"
	"errors"
	"testing"

	"github.com/gofiber/fiber/v2"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
)

// phaseMod records, in a shared journal, the order in which the core
// invoked its phases, and registers a teardown already from Setup.
type phaseMod struct {
	name    string
	journal *[]string
	failAt  string // "", "setup", "build" or "wire"
}

func (m *phaseMod) Name() string { return m.name }

func (m *phaseMod) Setup(_ context.Context, h Host) error {
	*m.journal = append(*m.journal, m.name+":setup")
	h.OnShutdown(func() error {
		*m.journal = append(*m.journal, m.name+":shutdown")
		return nil
	})
	if m.failAt == "setup" {
		return errors.New("boom")
	}
	return nil
}

func (m *phaseMod) Build(_ context.Context, _ Host) error {
	*m.journal = append(*m.journal, m.name+":build")
	if m.failAt == "build" {
		return errors.New("boom")
	}
	return nil
}

func (m *phaseMod) Wire(_ Host) error {
	*m.journal = append(*m.journal, m.name+":wire")
	if m.failAt == "wire" {
		return errors.New("boom")
	}
	return nil
}

func TestNew_PhaseOrder(t *testing.T) {
	var journal []string
	svc, err := New[struct{}, struct{}](context.Background(), Config{},
		WithMod(&phaseMod{name: "a", journal: &journal}),
		WithMod(&phaseMod{name: "b", journal: &journal}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	want := []string{
		"a:setup", "b:setup",
		"a:build", "b:build",
		"a:wire", "b:wire",
	}
	if len(journal) != len(want) {
		t.Fatalf("journal: want %v, got %v", want, journal)
	}
	for i := range want {
		if journal[i] != want[i] {
			t.Fatalf("journal[%d]: want %q, got %q (full: %v)", i, want[i], journal[i], journal)
		}
	}
}

func TestNew_BuildFailureUnwindsEarlierMods(t *testing.T) {
	var journal []string
	_, err := New[struct{}, struct{}](context.Background(), Config{},
		WithMod(&phaseMod{name: "ok", journal: &journal}),
		WithMod(&phaseMod{name: "bad", journal: &journal, failAt: "build"}))

	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeModBuildFailed {
		t.Fatalf("want Code=%q, got %#v", CodeModBuildFailed, err)
	}
	// Both mods registered OnShutdown in Setup — both must be torn
	// down, in LIFO order.
	last := journal[len(journal)-2:]
	if last[0] != "bad:shutdown" || last[1] != "ok:shutdown" {
		t.Fatalf("teardown not LIFO: %v", journal)
	}
}

func TestNew_DuplicateModNameIsCaughtBeforePhases(t *testing.T) {
	var journal []string
	_, err := New[struct{}, struct{}](context.Background(), Config{},
		WithMod(&phaseMod{name: "x", journal: &journal}),
		WithMod(&phaseMod{name: "x", journal: &journal}))

	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeModDuplicate {
		t.Fatalf("want Code=%q, got %#v", CodeModDuplicate, err)
	}
	if len(journal) != 0 {
		t.Fatalf("duplicate must be caught before phases, but journal is non-empty: %v", journal)
	}
}

// factoryMod registers a YAML factory from the Build phase.
type factoryMod struct{}

func (factoryMod) Name() string { return "factory" }

func (factoryMod) Build(_ context.Context, h Host) error {
	h.RegisterMiddlewareFactory("test_factory",
		func(args []string) (func(*fiber.Ctx) error, error) {
			return func(c *fiber.Ctx) error { return c.Next() }, nil
		})
	return nil
}

func TestNew_ModFactoryReachesEngine(t *testing.T) {
	svc, err := New[struct{}, struct{}](context.Background(), Config{}, WithMod(factoryMod{}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	// The YAML references the mod's factory; if the adapter didn't
	// work, Mount returns CodeUnknownMiddleware.
	yaml := []byte(`
groups:
  - routes:
      - method: GET
        path: /ping
        handler: ping
        middleware:
          - test_factory: []
`)
	// buildEngine (Task 5) does not install a default ContextBuilder —
	// v1 requires the same explicit call (see service/tls_test.go) —
	// so Mount needs one before it will validate the plan.
	svc.SetContextBuilder(func(c *fiber.Ctx) (struct{}, error) { return struct{}{}, nil })
	svc.Engine.RegisterHandler("ping", func(c *fibermap.Context[struct{}]) error {
		return c.Ctx.SendStatus(fiber.StatusOK)
	})
	if err := svc.Engine.LoadBytes(yaml); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	app := fiber.New()
	if err := svc.Engine.Mount(app); err != nil {
		t.Fatalf("Mount: mod factory did not reach the engine: %v", err)
	}
}
