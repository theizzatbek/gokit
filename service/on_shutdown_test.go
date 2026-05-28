package service

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestOnShutdown_RunsCallbacksInLIFOOrder(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{}}
	var order []int
	s.OnShutdown(func() error { order = append(order, 1); return nil })
	s.OnShutdown(func() error { order = append(order, 2); return nil })
	s.OnShutdown(func() error { order = append(order, 3); return nil })

	s.Close()

	want := []int{3, 2, 1}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i, v := range want {
		if order[i] != v {
			t.Errorf("order[%d] = %d, want %d", i, order[i], v)
		}
	}
}

func TestOnShutdown_ErrorIsLoggedAndDoesNotStopOthers(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, nil))
	s := &Service[struct{}, struct{}]{opts: &options{}, logger: logger}

	calls := 0
	s.OnShutdown(func() error { calls++; return nil })                // index 0
	s.OnShutdown(func() error { calls++; return errors.New("boom") }) // index 1
	s.OnShutdown(func() error { calls++; return nil })                // index 2

	s.Close()

	if calls != 3 {
		t.Errorf("calls = %d, want 3 (all callbacks must run even after one fails)", calls)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("logger did not capture the error: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "OnShutdown handler failed") {
		t.Errorf("logger missing event name: %q", buf.String())
	}
}

func TestOnShutdown_AfterCloseIsNoop(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{}}
	s.Close()

	called := false
	s.OnShutdown(func() error { called = true; return nil })

	// A subsequent Close (idempotent) should not invoke the post-close
	// callback because it was dropped.
	s.Close()
	if called {
		t.Errorf("OnShutdown registered after Close should not run")
	}
}

func TestClose_IsIdempotent(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{}}
	calls := 0
	s.OnShutdown(func() error { calls++; return nil })

	s.Close()
	s.Close() // second call must not re-invoke the callback

	if calls != 1 {
		t.Errorf("callback ran %d times across two Close calls, want 1", calls)
	}
}

func TestOnShutdown_NilFnIgnored(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{}}
	s.OnShutdown(nil) // must not panic
	s.Close()
}
