package svckit

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
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

func TestHost_HTTPCReturnsWhateverWasSet(t *testing.T) {
	h := &hostImpl{opts: &options{}}
	if got := h.HTTPC(); got != nil {
		t.Errorf("HTTPC(): want nil before it's set, got %v", got)
	}
	client := &http.Client{}
	h.httpc = client
	if got := h.HTTPC(); got != client {
		t.Error("HTTPC(): did not return the client set on hostImpl")
	}
}

func TestHost_ContextReturnsWhateverWasSet(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "marker")
	h := &hostImpl{opts: &options{}, ctx: ctx}
	if got := h.Context(); got != ctx {
		t.Error("Context(): did not return the ctx set on hostImpl")
	}
}

func TestHost_WrapCronJobAccumulatesInOrder(t *testing.T) {
	h := &hostImpl{opts: &options{}}
	var order []string

	h.WrapCronJob(func(next JobFn) JobFn {
		return func(ctx context.Context) error {
			order = append(order, "first")
			return next(ctx)
		}
	})
	h.WrapCronJob(func(next JobFn) JobFn {
		return func(ctx context.Context) error {
			order = append(order, "second")
			return next(ctx)
		}
	})
	h.WrapCronJob(nil) // must be ignored

	if got := len(h.opts.cronWrappers); got != 2 {
		t.Fatalf("cronWrappers: want 2, got %d", got)
	}

	svc := &Service[struct{}, struct{}]{opts: h.opts}
	wrapped := svc.wrapCronJob(func(ctx context.Context) error {
		order = append(order, "base")
		return nil
	})
	if err := wrapped(context.Background()); err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	// Last-registered decorator ("second") runs outermost per the
	// WrapCronJob doc; "first" wraps the base fn, "second" wraps "first".
	want := []string{"second", "first", "base"}
	if len(order) != len(want) {
		t.Fatalf("order: want %v, got %v", want, order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order[%d]: want %q, got %q (full: %v)", i, want[i], order[i], order)
		}
	}
}

func TestHost_RegisterMiddlewareFactory_DuplicateNameWarns(t *testing.T) {
	var buf bytes.Buffer
	h := &hostImpl{opts: &options{}, logger: slog.New(slog.NewTextHandler(&buf, nil))}

	h.RegisterMiddlewareFactory("dup",
		func(args []string) (func(*fiber.Ctx) error, error) { return nil, nil })
	h.RegisterMiddlewareFactory("dup",
		func(args []string) (func(*fiber.Ctx) error, error) { return nil, nil })

	if !strings.Contains(buf.String(), "more than once") {
		t.Errorf("expected a warning about the duplicate factory name, got log: %q", buf.String())
	}
	// Last-write-wins: still exactly one entry under the name.
	if got := len(h.opts.modFactories); got != 1 {
		t.Errorf("modFactories: want 1 entry, got %d", got)
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
