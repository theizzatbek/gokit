package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
)

// Tests for v1.1.0 P2-14: subcommand routing in service.Boot via
// WithSubcommand / BootSeed.

func TestResolveBootFn_NoOptions_ReturnsDefault(t *testing.T) {
	t.Parallel()
	defaultCalled := false
	defaultFn := func(context.Context) error { defaultCalled = true; return nil }

	fn := resolveBootFn(defaultFn, nil)
	_ = fn(context.Background())

	if !defaultCalled {
		t.Error("resolveBootFn with no opts returned a different fn (default not invoked)")
	}
}

func TestResolveBootFn_EmptySubcommandMap_ReturnsDefault(t *testing.T) {
	defer restoreArgs(t)
	os.Args = []string{"mybinary", "irrelevant"}

	defaultCalled := false
	defaultFn := func(context.Context) error { defaultCalled = true; return nil }

	// Option that doesn't actually register anything (e.g. a future
	// WithBootBanner). The default fn must still win.
	noop := BootOption(func(c *bootConfig) { /* no-op */ })
	fn := resolveBootFn(defaultFn, []BootOption{noop})
	_ = fn(context.Background())

	if !defaultCalled {
		t.Error("noop option leaked default-fn override")
	}
}

func TestResolveBootFn_RoutesToSubcommand(t *testing.T) {
	defer restoreArgs(t)
	os.Args = []string{"mybinary", "seed"}

	seedCalled := false
	defaultFn := func(context.Context) error { return errors.New("default should not run") }
	seedFn := func(context.Context) error { seedCalled = true; return nil }

	fn := resolveBootFn(defaultFn, []BootOption{
		BootSeed("seed", seedFn),
	})
	if err := fn(context.Background()); err != nil {
		t.Fatalf("fn err = %v, want nil", err)
	}
	if !seedCalled {
		t.Error("seed fn was not invoked")
	}
}

func TestResolveBootFn_UnknownArgFallsThroughToDefault(t *testing.T) {
	defer restoreArgs(t)
	os.Args = []string{"mybinary", "unknown-cmd"}

	defaultCalled := false
	defaultFn := func(context.Context) error { defaultCalled = true; return nil }
	seedFn := func(context.Context) error { return errors.New("seed should not run") }

	fn := resolveBootFn(defaultFn, []BootOption{BootSeed("seed", seedFn)})
	if err := fn(context.Background()); err != nil {
		t.Fatalf("fn err = %v, want nil", err)
	}
	if !defaultCalled {
		t.Error("default fn was not invoked on unknown arg")
	}
}

func TestResolveBootFn_NoArgsRunsDefault(t *testing.T) {
	defer restoreArgs(t)
	os.Args = []string{"mybinary"}

	defaultCalled := false
	defaultFn := func(context.Context) error { defaultCalled = true; return nil }

	fn := resolveBootFn(defaultFn, []BootOption{
		BootSeed("seed", func(context.Context) error { return errors.New("subcmd should not run") }),
	})
	if err := fn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !defaultCalled {
		t.Error("default fn was not invoked when no args supplied")
	}
}

func TestResolveBootFn_RoutesAmongMultipleSubcommands(t *testing.T) {
	defer restoreArgs(t)
	os.Args = []string{"mybinary", "migrate"}

	migrateCalled := false
	defaultFn := func(context.Context) error { return errors.New("default") }
	seedFn := func(context.Context) error { return errors.New("seed") }
	migrateFn := func(context.Context) error { migrateCalled = true; return nil }

	fn := resolveBootFn(defaultFn, []BootOption{
		BootSeed("seed", seedFn),
		WithSubcommand("migrate", migrateFn),
	})
	if err := fn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !migrateCalled {
		t.Error("migrate fn was not invoked for `mybinary migrate`")
	}
}

func TestResolveBootFn_LastWriteWins_OnDuplicateName(t *testing.T) {
	defer restoreArgs(t)
	os.Args = []string{"mybinary", "seed"}

	firstCalled := false
	secondCalled := false
	defaultFn := func(context.Context) error { return errors.New("default") }
	first := func(context.Context) error { firstCalled = true; return nil }
	second := func(context.Context) error { secondCalled = true; return nil }

	fn := resolveBootFn(defaultFn, []BootOption{
		BootSeed("seed", first),
		BootSeed("seed", second),
	})
	if err := fn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if firstCalled {
		t.Error("first registration won; expected last-write-wins")
	}
	if !secondCalled {
		t.Error("second registration didn't fire")
	}
}

// --- end-to-end through bootImpl (with injected exit/stderr) ---

func TestBootImpl_SubcommandError_TriggersExit(t *testing.T) {
	defer restoreArgs(t)
	os.Args = []string{"mybinary", "seed"}

	seedErr := errors.New("seed db connect failed")
	defaultFn := func(context.Context) error { return nil }
	seedFn := func(context.Context) error { return seedErr }

	var buf bytes.Buffer
	exitCode := -1
	exit := func(c int) { exitCode = c }

	fn := resolveBootFn(defaultFn, []BootOption{BootSeed("seed", seedFn)})
	bootImpl(fn, &buf, exit)

	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1", exitCode)
	}
	want := seedErr.Error() + "\n"
	if buf.String() != want {
		t.Errorf("stderr = %q, want %q", buf.String(), want)
	}
}

func restoreArgs(t *testing.T) {
	t.Helper()
	old := os.Args
	t.Cleanup(func() { os.Args = old })
}
