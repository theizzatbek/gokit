package natsclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/errs"
)

func TestEnsureStream_CreateAndIdempotentReapply(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	const name = "TEST_ENSURE_CREATE"
	t.Cleanup(func() { _ = c.DeleteStream(ctx, name) })

	cfg := StreamConfig{
		Name:     name,
		Subjects: []string{"tec.>"},
		MaxAge:   time.Hour,
	}
	if err := c.EnsureStream(ctx, cfg); err != nil {
		t.Fatalf("first EnsureStream: %v", err)
	}
	if err := c.EnsureStream(ctx, cfg); err != nil {
		t.Fatalf("re-EnsureStream: %v", err)
	}
}

func TestEnsureStream_DriftAppliesUpdate(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	const name = "TEST_ENSURE_DRIFT"
	t.Cleanup(func() { _ = c.DeleteStream(ctx, name) })

	_ = c.EnsureStream(ctx, StreamConfig{Name: name, Subjects: []string{"ted.>"}, MaxAge: time.Hour})
	if err := c.EnsureStream(ctx, StreamConfig{Name: name, Subjects: []string{"ted.>"}, MaxAge: 2 * time.Hour}); err != nil {
		t.Fatalf("update EnsureStream: %v", err)
	}
	si, err := c.JetStream().StreamInfo(name)
	if err != nil {
		t.Fatalf("StreamInfo: %v", err)
	}
	if si.Config.MaxAge != 2*time.Hour {
		t.Fatalf("MaxAge = %v, want 2h", si.Config.MaxAge)
	}
}

func TestEnsureStream_InvalidConfigErrors(t *testing.T) {
	c := newTestClient(t)
	err := c.EnsureStream(context.Background(), StreamConfig{Name: "TEST_INVALID"})
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("err type = %T", err)
	}
	if e.Code != CodeStreamConfigInvalid && e.Code != CodeStreamOpFailed {
		t.Fatalf("err code = %v, want stream_config_invalid or stream_op_failed", e.Code)
	}
}

func TestDeleteStream_IdempotentOnMissing(t *testing.T) {
	c := newTestClient(t)
	if err := c.DeleteStream(context.Background(), "DOES_NOT_EXIST_XYZ"); err != nil {
		t.Fatalf("delete missing should be no-op, got %v", err)
	}
}
