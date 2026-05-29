package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/auth"
)

// fakeStore implements auth.RefreshStore — the only methods exercised
// by startRefreshGC are GarbageCollect. Other methods return zero
// values so the type still satisfies the interface.
type fakeStore struct {
	gcCalls atomic.Int64
	gcN     int64
	gcErr   error
}

func (f *fakeStore) Issue(context.Context, auth.Record) error { return nil }
func (f *fakeStore) Consume(context.Context, [32]byte, time.Time) (auth.Record, error) {
	return auth.Record{}, nil
}
func (f *fakeStore) RevokeFamily(context.Context, string) error  { return nil }
func (f *fakeStore) RevokeSubject(context.Context, string) error { return nil }
func (f *fakeStore) GarbageCollect(_ context.Context, _ time.Time) (int64, error) {
	f.gcCalls.Add(1)
	return f.gcN, f.gcErr
}

// stubAuth is a non-nil *auth.Auth[C] sentinel. startRefreshGC only
// checks s.Auth != nil; it never invokes any method on it.
type stubClaims struct{}

func newStubAuth(t *testing.T) *auth.Auth[stubClaims] {
	t.Helper()
	keys, err := auth.GenerateEd25519Key("k1")
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.New[stubClaims](auth.Config{
		Issuer: "test", Keys: keys,
		AccessTTL: time.Minute, RefreshTTL: time.Hour,
	}, auth.WithRefreshStore(&fakeStore{}))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newServiceForGC(t *testing.T, interval time.Duration, store *fakeStore, logger *slog.Logger) *Service[struct{}, stubClaims] {
	t.Helper()
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service[struct{}, stubClaims]{
		opts:         &options{refreshGCInterval: interval},
		Auth:         newStubAuth(t),
		refreshStore: store,
		logger:       logger,
	}
}

func TestStartRefreshGC_TicksAndCallsGarbageCollect(t *testing.T) {
	store := &fakeStore{gcN: 7}
	s := newServiceForGC(t, 5*time.Millisecond, store, nil)
	s.startRefreshGC()

	// Wait for at least two ticks.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.gcCalls.Load() >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := store.gcCalls.Load(); got < 2 {
		t.Errorf("GarbageCollect was called %d times, want >= 2", got)
	}

	s.Close()
}

func TestStartRefreshGC_ShutdownStopsTicker(t *testing.T) {
	store := &fakeStore{}
	s := newServiceForGC(t, 5*time.Millisecond, store, nil)
	s.startRefreshGC()

	time.Sleep(30 * time.Millisecond)
	s.Close()
	mid := store.gcCalls.Load()

	time.Sleep(30 * time.Millisecond)
	final := store.gcCalls.Load()
	if final != mid {
		t.Errorf("calls kept incrementing after Close: %d → %d", mid, final)
	}
}

func TestStartRefreshGC_DisabledWhenIntervalZero(t *testing.T) {
	store := &fakeStore{}
	s := newServiceForGC(t, 0, store, nil)
	s.startRefreshGC()
	time.Sleep(30 * time.Millisecond)
	if got := store.gcCalls.Load(); got != 0 {
		t.Errorf("GarbageCollect called %d times with interval=0, want 0", got)
	}
	s.Close()
}

func TestStartRefreshGC_NoOpWhenAuthNil(t *testing.T) {
	store := &fakeStore{}
	s := &Service[struct{}, stubClaims]{
		opts:         &options{refreshGCInterval: 5 * time.Millisecond},
		Auth:         nil, // not configured
		refreshStore: store,
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	s.startRefreshGC()
	time.Sleep(30 * time.Millisecond)
	if got := store.gcCalls.Load(); got != 0 {
		t.Errorf("GarbageCollect called %d times with nil Auth, want 0", got)
	}
}

func TestStartRefreshGC_LogsRemovedCount(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	store := &fakeStore{gcN: 42}
	s := newServiceForGC(t, 10*time.Millisecond, store, logger)
	s.startRefreshGC()

	// Wait for at least one tick to fire.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.gcCalls.Load() >= 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	s.Close()

	out := buf.String()
	if !strings.Contains(out, "refresh GC") {
		t.Errorf("log missing event: %q", out)
	}
	if !strings.Contains(out, "removed=42") {
		t.Errorf("log missing removed count: %q", out)
	}
}

func TestStartRefreshGC_LogsErrors(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	store := &fakeStore{gcErr: errors.New("pg unreachable")}
	s := newServiceForGC(t, 10*time.Millisecond, store, logger)
	s.startRefreshGC()
	time.Sleep(50 * time.Millisecond)
	s.Close()

	out := buf.String()
	if !strings.Contains(out, "refresh GC failed") {
		t.Errorf("log missing failure event: %q", out)
	}
	if !strings.Contains(out, "pg unreachable") {
		t.Errorf("log missing wrapped err: %q", out)
	}
}
