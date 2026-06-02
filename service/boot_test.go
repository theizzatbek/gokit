package service

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestBoot_NilErrReturnsClean(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	exitCode := -1
	exit := func(c int) { exitCode = c }

	bootImpl(func(ctx context.Context) error { return nil }, &buf, exit)

	if exitCode != -1 {
		t.Errorf("exit was called (code %d) on nil err", exitCode)
	}
	if buf.Len() != 0 {
		t.Errorf("stderr written on nil err: %q", buf.String())
	}
}

func TestBoot_ContextCanceledIsGraceful(t *testing.T) {
	t.Parallel()
	// Graceful shutdown via signal returns ctx.Err()==context.Canceled.
	// Boot must treat that as success — no stderr, no os.Exit.
	var buf bytes.Buffer
	exitCode := -1
	exit := func(c int) { exitCode = c }

	bootImpl(func(ctx context.Context) error {
		return context.Canceled
	}, &buf, exit)

	if exitCode != -1 {
		t.Errorf("exit was called (code %d) on context.Canceled", exitCode)
	}
	if buf.Len() != 0 {
		t.Errorf("stderr written on context.Canceled: %q", buf.String())
	}
}

func TestBoot_WrappedContextCanceledIsGraceful(t *testing.T) {
	t.Parallel()
	// errors.Is must walk the wrap chain.
	var buf bytes.Buffer
	exitCode := -1
	exit := func(c int) { exitCode = c }

	wrapped := errors.New("server stopped: " + context.Canceled.Error())
	_ = wrapped // unused — instead use real wrap
	err := fakeWrap(context.Canceled)

	bootImpl(func(ctx context.Context) error { return err }, &buf, exit)

	if exitCode != -1 {
		t.Errorf("exit was called (code %d) on wrapped context.Canceled", exitCode)
	}
	if buf.Len() != 0 {
		t.Errorf("stderr written: %q", buf.String())
	}
}

func TestBoot_RealErrorTriggersExit(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	exitCode := -1
	exit := func(c int) { exitCode = c }

	boom := errors.New("upstream API unreachable")
	bootImpl(func(ctx context.Context) error { return boom }, &buf, exit)

	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1", exitCode)
	}
	out := buf.String()
	if out == "" {
		t.Error("stderr was empty on real error")
	}
	// The error is printed verbatim followed by a newline.
	want := boom.Error() + "\n"
	if out != want {
		t.Errorf("stderr = %q, want %q", out, want)
	}
}

func TestBoot_FnReceivesSignalAwareContext(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	captured := false
	bootImpl(func(ctx context.Context) error {
		// The ctx must NOT be context.Background() — it must be a
		// derived context that listens for signals. We can't easily
		// fire SIGINT inside a test, but we can assert it's a
		// non-Background ctx by checking the cancel-causality chain.
		if ctx == context.Background() {
			t.Error("Boot passed plain context.Background, expected NotifyContext")
		}
		// Confirm ctx.Done() is non-nil (background's Done is nil).
		if ctx.Done() == nil {
			t.Error("ctx.Done() is nil — NotifyContext did not wire a Done chan")
		}
		captured = true
		return nil
	}, &buf, func(int) {})

	if !captured {
		t.Error("fn was not invoked")
	}
}

// fakeWrap returns err inside a struct that satisfies errors.Is via
// its Unwrap method — a minimal stand-in for fmt.Errorf("%w").
func fakeWrap(err error) error {
	return &wrapErr{err: err}
}

type wrapErr struct{ err error }

func (w *wrapErr) Error() string { return "wrapped: " + w.err.Error() }
func (w *wrapErr) Unwrap() error { return w.err }
