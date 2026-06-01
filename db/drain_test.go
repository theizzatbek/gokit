package db_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/db"
)

func TestDrain_IdleReturnsImmediately(t *testing.T) {
	d := startTestDB(t)
	start := time.Now()
	if err := d.Drain(context.Background()); err != nil {
		t.Fatalf("Drain on idle pool: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("Drain took %v on idle pool, want < 200ms", elapsed)
	}
}

func TestDrain_WaitsForInFlightThenCompletes(t *testing.T) {
	d := startTestDB(t)
	// Hold a conn busy in a separate goroutine.
	busy := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = d.Tx(context.Background(), func(tx *db.Tx) error {
			close(busy)
			time.Sleep(300 * time.Millisecond)
			return nil
		})
	}()

	<-busy
	start := time.Now()
	if err := d.Drain(context.Background()); err != nil {
		t.Fatalf("Drain waiting for in-flight: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Errorf("Drain completed in %v — should have waited for the in-flight Tx (~300ms)", elapsed)
	}
	<-done
}

func TestDrain_CtxTimeoutClosesAnyway(t *testing.T) {
	d := startTestDB(t)
	// Hold conn longer than Drain deadline.
	busy := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_ = d.Tx(context.Background(), func(tx *db.Tx) error {
			close(busy)
			time.Sleep(2 * time.Second)
			return nil
		})
	}()

	<-busy
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := d.Drain(ctx)
	if err == nil {
		t.Error("Drain should have returned ctx error on timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
	<-finished
}

func TestDrain_NilReceiverSafe(t *testing.T) {
	var d *db.DB
	if err := d.Drain(context.Background()); err != nil {
		t.Errorf("nil Drain = %v, want nil", err)
	}
}

func TestDrain_IdempotentAfterClose(t *testing.T) {
	d := startTestDB(t)
	d.Close()
	// Second Drain after explicit Close must not panic.
	if err := d.Drain(context.Background()); err != nil {
		t.Errorf("Drain after Close = %v, want nil", err)
	}
}

func TestDrain_ConcurrentSafe(t *testing.T) {
	d := startTestDB(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.Drain(context.Background())
		}()
	}
	wg.Wait()
}
