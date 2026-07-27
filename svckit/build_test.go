package svckit

import (
	"context"
	"errors"
	"net/http"
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

// TestNew_SetupFailureUnwindsEarlierMods mirrors
// TestNew_BuildFailureUnwindsEarlierMods for the Setup phase. Setup's
// unwind path is structurally different from Build/Wire's: the
// bulk-copy of Host-accumulated shutdown callbacks onto Service
// happens once, right after the whole Setup loop finishes (see
// build.go), rather than per-mod inside the loop like Build/Wire.
// A mod failing Setup before that bulk-copy runs must still see its
// own (and every earlier mod's) OnShutdown honoured.
func TestNew_SetupFailureUnwindsEarlierMods(t *testing.T) {
	var journal []string
	_, err := New[struct{}, struct{}](context.Background(), Config{},
		WithMod(&phaseMod{name: "ok", journal: &journal}),
		WithMod(&phaseMod{name: "bad", journal: &journal, failAt: "setup"}))

	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeModSetupFailed {
		t.Fatalf("want Code=%q, got %#v", CodeModSetupFailed, err)
	}
	// Neither mod reached :build or :wire.
	for _, entry := range journal {
		if entry == "ok:build" || entry == "ok:wire" || entry == "bad:build" || entry == "bad:wire" {
			t.Fatalf("phase after setup ran despite setup failure: %v", journal)
		}
	}
	// Both mods registered OnShutdown from their own Setup — both must
	// be torn down, in LIFO order.
	last := journal[len(journal)-2:]
	if last[0] != "bad:shutdown" || last[1] != "ok:shutdown" {
		t.Fatalf("teardown not LIFO: %v", journal)
	}
}

// TestNew_WireFailureUnwindsEarlierMods mirrors
// TestNew_BuildFailureUnwindsEarlierMods for the Wire phase — the
// same phase Fix #1 (Wire-time RegisterMiddlewareFactory) touches, so
// this is the regression anchor for that fix: a mod failing during
// Wire must still unwind cleanly even though registerModFactories now
// runs a second time right after the Wire loop.
func TestNew_WireFailureUnwindsEarlierMods(t *testing.T) {
	var journal []string
	_, err := New[struct{}, struct{}](context.Background(), Config{},
		WithMod(&phaseMod{name: "ok", journal: &journal}),
		WithMod(&phaseMod{name: "bad", journal: &journal, failAt: "wire"}))

	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeModWireFailed {
		t.Fatalf("want Code=%q, got %#v", CodeModWireFailed, err)
	}
	// Both mods reached build (Wire fails after Build completes for
	// everyone), but "bad" itself never finished wiring.
	want := []string{
		"ok:setup", "bad:setup",
		"ok:build", "bad:build",
		"ok:wire", "bad:wire",
	}
	if len(journal) < len(want) {
		t.Fatalf("journal shorter than expected: %v", journal)
	}
	for i := range want {
		if journal[i] != want[i] {
			t.Fatalf("journal[%d]: want %q, got %q (full: %v)", i, want[i], journal[i], journal)
		}
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

// hostProbeMod captures what Host.HTTPC() and Host.Context() return
// at each phase, so New's wiring of those two Host additions can be
// checked end to end rather than only at the hostImpl unit level.
type hostProbeMod struct {
	httpcAtSetup, httpcAtBuild *http.Client
	ctxAtSetup, ctxAtBuild     context.Context
}

func (*hostProbeMod) Name() string { return "host_probe" }

func (m *hostProbeMod) Setup(_ context.Context, h Host) error {
	m.httpcAtSetup = h.HTTPC()
	m.ctxAtSetup = h.Context()
	return nil
}

func (m *hostProbeMod) Build(_ context.Context, h Host) error {
	m.httpcAtBuild = h.HTTPC()
	m.ctxAtBuild = h.Context()
	return nil
}

func TestNew_HostExposesHTTPCAndContext(t *testing.T) {
	probe := &hostProbeMod{}
	svc, err := New[struct{}, struct{}](context.Background(), Config{}, WithMod(probe))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	if probe.httpcAtSetup != nil {
		t.Error("Host.HTTPC() during Setup: want nil (buildHTTPC hasn't run yet), got non-nil")
	}
	if probe.httpcAtBuild == nil {
		t.Fatal("Host.HTTPC() during Build: want the kit-built client, got nil")
	}
	if probe.httpcAtBuild != svc.HTTPC {
		t.Error("Host.HTTPC() during Build did not return the same client as svc.HTTPC")
	}

	if probe.ctxAtSetup == nil {
		t.Fatal("Host.Context() during Setup: want the service's runCtx, got nil")
	}
	if probe.ctxAtSetup != probe.ctxAtBuild {
		t.Error("Host.Context() changed between Setup and Build — it must be the one long-lived runCtx")
	}
	if probe.ctxAtSetup != svc.runCtx {
		t.Error("Host.Context() did not return svc.runCtx")
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

// wireFactoryMod registers a YAML factory from the Wire phase instead
// of Build — the phase Fix #1 is about. Before the fix,
// registerModFactories ran once, BEFORE the Wire loop, so anything a
// mod registered from Wire sat unread in opts.modFactories and Mount
// would fail with CodeUnknownMiddleware instead of ever reaching the
// engine. This is exactly the shape v1's rate_limit_redis wiring
// takes (service/ratelimit.go mounts its factory after buildEngine,
// i.e. in what would be the Wire-equivalent step) — the case the task
// description calls out as the next mod to hit this gap.
type wireFactoryMod struct{}

func (wireFactoryMod) Name() string { return "wire_factory" }

func (wireFactoryMod) Wire(h Host) error {
	h.RegisterMiddlewareFactory("wire_test_factory",
		func(args []string) (func(*fiber.Ctx) error, error) {
			return func(c *fiber.Ctx) error { return c.Next() }, nil
		})
	return nil
}

func TestNew_WireModFactoryReachesEngine(t *testing.T) {
	svc, err := New[struct{}, struct{}](context.Background(), Config{}, WithMod(wireFactoryMod{}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	// Same YAML shape as TestNew_ModFactoryReachesEngine — Mount needs
	// an explicit ContextBuilder before it will validate the plan.
	yaml := []byte(`
groups:
  - routes:
      - method: GET
        path: /ping
        handler: ping
        middleware:
          - wire_test_factory: []
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
		t.Fatalf("Mount: Wire-phase mod factory did not reach the engine: %v", err)
	}
}
