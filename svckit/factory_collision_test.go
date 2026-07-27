package svckit

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/gokit/fibermap"
)

// dupFactoryMod registers a fixed factory name ("shared_factory")
// from whichever single phase its `phase` field names. Two instances
// with different Name()s (mod names must be unique — validateMods
// enforces that separately from factory names) but the same `phase`
// simulate a same-phase collision; different phases simulate the
// cross-phase collision that used to reach fibermap's own
// duplicate-registration panic before this fix.
type dupFactoryMod struct {
	name  string
	phase string // "build" or "wire"
}

func (m dupFactoryMod) Name() string { return m.name }

func (m dupFactoryMod) register(h Host) {
	h.RegisterMiddlewareFactory("shared_factory",
		func(args []string) (func(*fiber.Ctx) error, error) {
			return func(c *fiber.Ctx) error { return c.Next() }, nil
		})
}

func (m dupFactoryMod) Build(_ context.Context, h Host) error {
	if m.phase == "build" {
		m.register(h)
	}
	return nil
}

func (m dupFactoryMod) Wire(h Host) error {
	if m.phase == "wire" {
		m.register(h)
	}
	return nil
}

// mountsSharedFactory drives a built Service through Mount with a
// routes.yaml referencing "shared_factory", proving whichever
// registration won is actually wired into the engine (not just
// present in some internal map).
func mountsSharedFactory(t *testing.T, svc *Service[struct{}, struct{}]) {
	t.Helper()
	yaml := []byte(`
groups:
  - routes:
      - method: GET
        path: /ping
        handler: ping
        middleware:
          - shared_factory: []
`)
	svc.SetContextBuilder(func(c *fiber.Ctx) (struct{}, error) { return struct{}{}, nil })
	svc.Engine.RegisterHandler("ping", func(c *fibermap.Context[struct{}]) error {
		return c.Ctx.SendStatus(fiber.StatusOK)
	})
	if err := svc.Engine.LoadBytes(yaml); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	app := fiber.New()
	if err := svc.Engine.Mount(app); err != nil {
		t.Fatalf("Mount: %v", err)
	}
}

// TestNew_DuplicateFactoryName_SamePhase_WarnsAndSucceeds is the
// baseline this fix must not regress: two mods claiming the same
// factory name from the SAME phase already warned and succeeded
// before the cross-phase fix (the collision is visible in
// opts.modFactories directly, no modFactoriesDone lookup needed).
func TestNew_DuplicateFactoryName_SamePhase_WarnsAndSucceeds(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	svc, err := New[struct{}, struct{}](context.Background(), Config{},
		WithLogger(logger),
		WithMod(dupFactoryMod{name: "mod-a", phase: "build"}),
		WithMod(dupFactoryMod{name: "mod-b", phase: "build"}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	if !strings.Contains(buf.String(), "more than once") {
		t.Errorf("expected a warning about the duplicate factory name, got log: %q", buf.String())
	}
	mountsSharedFactory(t, svc)
}

// TestNew_DuplicateFactoryName_CrossPhase_DoesNotPanic is the
// regression anchor for the cross-phase collision: registerModFactories
// now runs twice (Fix #1), so a name registered during Build is
// already deleted from opts.modFactories — and, pre-this-fix, no
// longer visible to hostImpl.RegisterMiddlewareFactory's duplicate
// check — by the time a second mod registers the SAME name from
// Wire. That let the second registration slip back into
// opts.modFactories unchallenged, and the second registerModFactories
// pass (after Wire) then called Engine.RegisterMiddlewareFactory with
// a name fibermap already had, which panics
// (CodeDuplicateRegistration). New must not let that panic escape:
// same diagnosable warning, same successful construction, regardless
// of which phases the two registrations land in.
func TestNew_DuplicateFactoryName_CrossPhase_DoesNotPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	var svc *Service[struct{}, struct{}]
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("New panicked on a cross-phase factory-name collision (Build vs Wire): %v", r)
			}
		}()
		svc, err = New[struct{}, struct{}](context.Background(), Config{},
			WithLogger(logger),
			WithMod(dupFactoryMod{name: "mod-a", phase: "build"}),
			WithMod(dupFactoryMod{name: "mod-b", phase: "wire"}),
		)
	}()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	if !strings.Contains(buf.String(), "more than once") {
		t.Errorf("expected a warning about the cross-phase duplicate factory name, got log: %q", buf.String())
	}
	mountsSharedFactory(t, svc)
}
