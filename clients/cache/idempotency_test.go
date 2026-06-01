package cache_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/clients/cache"
	redisclient "github.com/theizzatbek/gokit/clients/redis"
	"github.com/theizzatbek/gokit/fibermap"
)

func newTestIdemStore(t *testing.T) *cache.RedisIdempotencyStore {
	t.Helper()
	rc, err := redisclient.Connect(context.Background(), redisclient.Config{URL: testRedisURL})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = rc.Close() })
	return cache.NewIdempotencyStore(rc, "idem-test:")
}

func TestIdempotencyStore_GetSetRoundTrip(t *testing.T) {
	flushRedis(t)
	s := newTestIdemStore(t)
	ctx := context.Background()
	in := &fibermap.StoredResponse{Status: 201, Body: []byte(`{"ok":true}`), ContentType: "application/json"}
	if err := s.Set(ctx, "k", in, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	out, err := s.Get(ctx, "k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if out == nil || out.Status != 201 || string(out.Body) != `{"ok":true}` {
		t.Errorf("Get = %+v, want round-tripped StoredResponse", out)
	}
}

func TestIdempotencyStore_AcquireLockReleasesLock(t *testing.T) {
	flushRedis(t)
	s := newTestIdemStore(t)
	ctx := context.Background()

	ok, err := s.AcquireLock(ctx, "k1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("first acquire = (%v, %v), want (true, nil)", ok, err)
	}

	// Second concurrent attempt must fail while lock is held.
	ok, err = s.AcquireLock(ctx, "k1", time.Minute)
	if err != nil || ok {
		t.Fatalf("second acquire while held = (%v, %v), want (false, nil)", ok, err)
	}

	// Release allows re-acquire.
	if err := s.ReleaseLock(ctx, "k1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	ok, err = s.AcquireLock(ctx, "k1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("acquire after release = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestIdempotencyStore_LockTTLExpires(t *testing.T) {
	flushRedis(t)
	s := newTestIdemStore(t)
	ctx := context.Background()

	if ok, _ := s.AcquireLock(ctx, "k2", 300*time.Millisecond); !ok {
		t.Fatal("first acquire failed")
	}
	if ok, _ := s.AcquireLock(ctx, "k2", time.Second); ok {
		t.Fatal("acquire succeeded before TTL elapsed")
	}
	time.Sleep(350 * time.Millisecond)
	if ok, _ := s.AcquireLock(ctx, "k2", time.Second); !ok {
		t.Error("acquire after TTL elapsed = false, want true")
	}
}

func TestIdempotencyStore_AcquireLockUnderConcurrency(t *testing.T) {
	flushRedis(t)
	s := newTestIdemStore(t)
	ctx := context.Background()

	const goroutines = 50
	var winners int64
	done := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			if ok, _ := s.AcquireLock(ctx, "race", time.Second); ok {
				atomic.AddInt64(&winners, 1)
			}
		}()
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
	if got := atomic.LoadInt64(&winners); got != 1 {
		t.Errorf("winners = %d, want exactly 1 under concurrency", got)
	}
}

func TestIdempotencyStore_NilReceiverSafe(t *testing.T) {
	var s *cache.RedisIdempotencyStore
	if _, err := s.AcquireLock(context.Background(), "k", time.Second); err != nil {
		t.Errorf("nil AcquireLock returned err: %v", err)
	}
	if err := s.ReleaseLock(context.Background(), "k"); err != nil {
		t.Errorf("nil ReleaseLock returned err: %v", err)
	}
}

// compile-time check: RedisIdempotencyStore implements both store + locker.
var (
	_ fibermap.IdempotencyStore  = (*cache.RedisIdempotencyStore)(nil)
	_ fibermap.IdempotencyLocker = (*cache.RedisIdempotencyStore)(nil)
)
