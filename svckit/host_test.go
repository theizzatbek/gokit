package svckit

import (
	"log/slog"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestHost_AccumulatesModRequests(t *testing.T) {
	h := &hostImpl{opts: &options{}, logger: slog.Default()}

	h.UseFiber(func(c *fiber.Ctx) error { return nil })
	h.RegisterMiddlewareFactory("test_factory",
		func(args []string) (func(*fiber.Ctx) error, error) { return nil, nil })
	h.OnShutdown(func() error { return nil })

	if got := len(h.opts.fiberMiddleware); got != 1 {
		t.Errorf("fiberMiddleware: want 1, got %d", got)
	}
	if _, ok := h.opts.modFactories["test_factory"]; !ok {
		t.Error("modFactories: factory was not registered")
	}
	if got := len(h.opts.shutdownFns); got != 1 {
		t.Errorf("shutdownFns: want 1, got %d", got)
	}
}

func TestHost_OnShutdownIgnoresNil(t *testing.T) {
	h := &hostImpl{opts: &options{}, logger: slog.Default()}

	h.OnShutdown(nil)

	if got := len(h.opts.shutdownFns); got != 0 {
		t.Errorf("nil callback must not accumulate: got %d", got)
	}
}

func TestHost_SetLoggerIsVisibleToLaterMods(t *testing.T) {
	h := &hostImpl{opts: &options{}, logger: slog.Default()}
	custom := slog.Default().With("wrapped", true)

	h.SetLogger(custom)

	if h.Logger() != custom {
		t.Error("SetLogger did not change what Logger() returns")
	}
}

func TestHost_ResolvePathHonoursConfigsDir(t *testing.T) {
	h := &hostImpl{opts: &options{}}
	h.cfg.Service.ConfigsDir = "configs"

	if got := h.ResolvePath("", "crons.yaml", true); got != "configs/crons.yaml" {
		t.Errorf("want configs/crons.yaml, got %q", got)
	}
	if got := h.ResolvePath("/etc/crons.yaml", "crons.yaml", true); got != "/etc/crons.yaml" {
		t.Errorf("an explicit path must win: got %q", got)
	}
	if got := h.ResolvePath("", "crons.yaml", false); got != "" {
		t.Errorf("disabled subsystem → empty, got %q", got)
	}
}
