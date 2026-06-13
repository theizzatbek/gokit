package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Boot is the main()-boilerplate reducer for kit-based services. It:
//
//  1. Wraps [context.Background] with [signal.NotifyContext] for
//     SIGINT + SIGTERM, so SIGTERM from Kubernetes / docker stop
//     cancels ctx cleanly.
//  2. If [WithSubcommand] / [BootSeed] options were passed AND the
//     first command-line arg matches a registered subcommand name,
//     dispatches to that subcommand instead of fn. This is the
//     "./mybinary seed" pattern used by every kit-based service
//     eventually growing a CLI seed / migrate / inspect mode.
//  3. Otherwise calls fn with the signal-aware context.
//  4. Distinguishes graceful exit from failure:
//     - fn (or the subcommand) returns nil OR [context.Canceled] →
//     process exits 0 (signal was the cause; service shut down as
//     designed).
//     - fn returns any other error → prints to stderr and exits 1.
//
// Boot does NOT construct a logger — every service has its own opinion
// on format / level / handler. fn owns logger creation (or accepts one
// from a parent caller).
//
// Typical usage (no subcommands):
//
//	func main() {
//	    service.Boot(run)
//	}
//
//	func run(ctx context.Context) error {
//	    cfg, err := config.Load()
//	    if err != nil { return err }
//	    svc, err := service.New[AppCtx, MyClaims](ctx, cfg, ...)
//	    if err != nil { return err }
//	    defer svc.Close()
//	    // ... wire handlers ...
//	    return svc.Run()
//	}
//
// Subcommand usage (seed mode):
//
//	func main() {
//	    service.Boot(run,
//	        service.BootSeed("seed", seed),
//	        service.WithSubcommand("migrate", migrate),
//	    )
//	}
//
//	// Invocations:
//	//   ./mybinary           → run
//	//   ./mybinary seed      → seed
//	//   ./mybinary migrate   → migrate
//	//   ./mybinary anything  → run (unknown subcommand falls through)
//
// Subcommand fns share the same signature as the default fn —
// `func(ctx context.Context) error` — so each owns its own [New]
// invocation. The kit deliberately does NOT pass a pre-constructed
// Service to subcommands: building the Service requires type
// parameters Boot can't carry, and the seed / migrate / inspect
// paths typically want different option sets than the production run
// path (e.g. WithoutCron, WithoutOpenAPI, but WithMigrations).
//
// Boot exits the process via [os.Exit] on the failure path. main's own
// deferred cleanup is skipped on that path (a known Go quirk of
// os.Exit) — put deferred work inside fn instead.
func Boot(fn func(ctx context.Context) error, opts ...BootOption) {
	bootImpl(resolveBootFn(fn, opts), os.Stderr, os.Exit)
}

// BootOption configures [Boot] — currently the only kind is
// subcommand-handler registration via [WithSubcommand] / [BootSeed].
type BootOption func(*bootConfig)

// bootConfig is the accumulated state of all [BootOption]s. Empty
// when Boot is called without options, in which case the default fn
// always runs.
type bootConfig struct {
	subcommands map[string]func(ctx context.Context) error
}

// WithSubcommand registers a handler dispatched when os.Args[1]
// matches name (case-sensitive, exact match). When the user invokes
// `./mybinary <name>`, fn runs instead of Boot's default fn. Other
// argument values (including no arguments) fall through to the
// default.
//
// Multiple WithSubcommand calls accumulate. The same name registered
// twice on the same Boot call has last-write-wins semantics —
// callers should treat duplicate names as a programmer error.
//
// fn receives the same signal-aware context Boot would have passed
// to the default fn, so SIGINT / SIGTERM during a long-running
// seed / migrate operation propagate cleanly.
func WithSubcommand(name string, fn func(ctx context.Context) error) BootOption {
	return func(c *bootConfig) {
		if c.subcommands == nil {
			c.subcommands = make(map[string]func(ctx context.Context) error)
		}
		c.subcommands[name] = fn
	}
}

// BootSeed is a named alias for [WithSubcommand]. Identical
// semantics; the alias signals intent at the call site (every
// kit-based service eventually grows a `seed` subcommand for demo
// data / fixtures, and the named form reads better than
// WithSubcommand("seed", ...) in main.go).
//
// Use [WithSubcommand] directly for non-seed names (migrate,
// inspect, schema-dump, etc).
func BootSeed(name string, fn func(ctx context.Context) error) BootOption {
	return WithSubcommand(name, fn)
}

// resolveBootFn picks the effective fn for [Boot]: a registered
// subcommand if os.Args[1] matches, else the default fn. Pure
// function over (default, options, os.Args) so testable without
// touching globals.
func resolveBootFn(defaultFn func(ctx context.Context) error, opts []BootOption) func(ctx context.Context) error {
	if len(opts) == 0 {
		return defaultFn
	}
	var c bootConfig
	for _, opt := range opts {
		opt(&c)
	}
	if len(c.subcommands) == 0 || len(os.Args) < 2 {
		return defaultFn
	}
	if fn, ok := c.subcommands[os.Args[1]]; ok {
		return fn
	}
	return defaultFn
}

// bootImpl is the testable inner form. Production callers use [Boot];
// tests inject errOut + exit to capture behaviour without actually
// terminating the test runner.
func bootImpl(fn func(ctx context.Context) error, errOut io.Writer, exit func(int)) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	err := fn(ctx)
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	fmt.Fprintln(errOut, err)
	exit(1)
}
