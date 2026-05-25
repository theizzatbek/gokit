package natsclient

import (
	"context"
	"errors"
	"testing"

	"github.com/theizzatbek/fibermap/errs"
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
