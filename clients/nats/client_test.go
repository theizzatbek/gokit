package natsclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/theizzatbek/gokit/errs"
)

func TestConnect_RejectsMissingURL(t *testing.T) {
	_, err := Connect(context.Background(), Config{})
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		t.Fatalf("expected Validation, got %v", err)
	}
}

func TestConnect_RejectsAuthAmbiguous(t *testing.T) {
	_, err := Connect(context.Background(), Config{
		URL: "nats://localhost:4222", Token: "t", CredsFile: "/tmp/c",
	})
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != CodeAuthAmbiguous {
		t.Fatalf("expected auth_ambiguous, got %v", err)
	}
}

func TestConnect_FailsOnUnreachableAddress(t *testing.T) {
	_, err := Connect(context.Background(), Config{URL: "nats://127.0.0.1:1"})
	if err == nil {
		t.Fatalf("expected error connecting to dead address")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != CodeConnectFailed {
		t.Fatalf("err code = %v, want %s", err, CodeConnectFailed)
	}
}

func TestOptions_ReconnectHandlersCompile(t *testing.T) {
	c, err := Connect(context.Background(), Config{URL: "nats://127.0.0.1:1"},
		WithReconnectHandler(func(*nats.Conn) {}),
		WithDisconnectErrHandler(func(*nats.Conn, error) {}),
		WithClosedHandler(func(*nats.Conn) {}),
	)
	if err == nil {
		c.Close()
		t.Fatal("expected error connecting to dead address")
	}
}

func TestConnect_FailsAfterBudget(t *testing.T) {
	cfg := Config{
		URL:                "nats://127.0.0.1:1", // unreachable
		Timeout:            50 * time.Millisecond,
		ConnectMaxRetries:  2,
		ConnectBackoffBase: 10 * time.Millisecond,
		ConnectBackoffMax:  20 * time.Millisecond,
	}
	start := time.Now()
	_, err := Connect(context.Background(), cfg)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error")
	}
	// 2 retries × (50ms timeout + 20ms backoff) is well under 2s.
	if elapsed > 2*time.Second {
		t.Fatalf("budget exceeded: %v", elapsed)
	}
}

func TestConnect_CtxCancelDuringBackoff(t *testing.T) {
	cfg := Config{
		URL:                "nats://127.0.0.1:1",
		Timeout:            50 * time.Millisecond,
		ConnectMaxRetries:  100,
		ConnectBackoffBase: 100 * time.Millisecond,
		ConnectBackoffMax:  1 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Connect(ctx, cfg)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error")
	}
	// Must abort early — much less than MaxRetries * backoff cap.
	if elapsed > 2*time.Second {
		t.Fatalf("did not abort on ctx cancel: %v", elapsed)
	}
}
