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
//  2. Calls fn with the signal-aware context.
//  3. Distinguishes graceful exit from failure:
//     - fn returns nil OR [context.Canceled] → process exits 0 (signal
//     was the cause; service shut down as designed).
//     - fn returns any other error → prints to stderr and exits 1.
//
// Boot does NOT construct a logger — every service has its own opinion
// on format / level / handler. fn owns logger creation (or accepts one
// from a parent caller).
//
// Typical usage:
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
// Boot exits the process via [os.Exit] on the failure path. main's own
// deferred cleanup is skipped on that path (a known Go quirk of
// os.Exit) — put deferred work inside fn instead.
func Boot(fn func(ctx context.Context) error) {
	bootImpl(fn, os.Stderr, os.Exit)
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
