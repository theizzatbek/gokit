package sentrykit_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/theizzatbek/gokit/sentrykit"
)

// testDSN is a syntactically valid public DSN; sentrykit.Setup
// validates the format but never sends to it because every test
// installs a BeforeSend hook that captures + returns nil (event
// dropped before transport).
const testDSN = "https://public@o0.ingest.sentry.io/0"

func TestSetup_RequiresDSN(t *testing.T) {
	_, err := sentrykit.Setup(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty DSN, got nil")
	}
	if !strings.Contains(err.Error(), "dsn") {
		t.Errorf("err = %v, expected mention of dsn", err)
	}
}

func TestSetup_ShutdownIsIdempotent(t *testing.T) {
	shutdown, err := sentrykit.Setup(context.Background(), testDSN,
		sentrykit.WithBeforeSend(func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event { return nil }),
	)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Errorf("first shutdown: %v", err)
	}
	if err := shutdown(ctx); err != nil {
		t.Errorf("second shutdown: %v", err)
	}
}

func TestSetup_BeforeSendFiresWithEnvironmentReleaseAndTags(t *testing.T) {
	var (
		mu     sync.Mutex
		events []*sentry.Event
	)
	shutdown, err := sentrykit.Setup(context.Background(), testDSN,
		sentrykit.WithEnvironment("staging"),
		sentrykit.WithRelease("svc@1.2.3"),
		sentrykit.WithTag("region", "us-east-1"),
		sentrykit.WithBeforeSend(func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			mu.Lock()
			events = append(events, e)
			mu.Unlock()
			return nil // drop before network send
		}),
	)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_ = shutdown(ctx)
	})

	sentry.CaptureMessage("hello-from-test")

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Environment != "staging" {
		t.Errorf("Environment = %q, want staging", e.Environment)
	}
	if e.Release != "svc@1.2.3" {
		t.Errorf("Release = %q, want svc@1.2.3", e.Release)
	}
	if e.Tags["region"] != "us-east-1" {
		t.Errorf("Tags[region] = %q, want us-east-1", e.Tags["region"])
	}
	if e.Message != "hello-from-test" {
		t.Errorf("Message = %q, want hello-from-test", e.Message)
	}
}

func TestSetup_FlushHonoursCtxDeadline(t *testing.T) {
	// The shutdown function derives its Flush timeout from the
	// supplied context's deadline. A 1ms deadline must NOT block the
	// caller for the default 5s.
	shutdown, err := sentrykit.Setup(context.Background(), testDSN,
		sentrykit.WithBeforeSend(func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event { return nil }),
	)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = shutdown(ctx)
		close(done)
	}()
	select {
	case <-done:
		// pass
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown blocked past the ctx deadline + slack")
	}
}
