package natsclient

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSubscribe_RoundTrip(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const stream = "TEST_SUB_RT"
	t.Cleanup(func() { _ = c.DeleteStream(ctx, stream) })

	_ = c.EnsureStream(ctx, StreamConfig{Name: stream, Subjects: []string{"subrt.>"}})

	got := make(chan orderCreated, 1)
	sub, err := Subscribe[orderCreated](ctx, c, "subrt.created",
		func(_ context.Context, m Msg[orderCreated]) error {
			got <- m.Data
			return nil
		},
		WithDurable("subrt-d1"),
	)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Drain() })

	pub := NewPublisher[orderCreated](c)
	want := orderCreated{ID: "o-1", Amount: 100}
	if err := pub.Publish(ctx, "subrt.created", want); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case v := <-got:
		if v != want {
			t.Fatalf("got %+v, want %+v", v, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for delivery")
	}
}

func TestSubscribe_DeliversMsgMetadata(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	const stream = "TEST_SUB_META"
	t.Cleanup(func() { _ = c.DeleteStream(ctx, stream) })
	_ = c.EnsureStream(ctx, StreamConfig{Name: stream, Subjects: []string{"submeta.>"}})

	var (
		mu      sync.Mutex
		gotMsg  Msg[orderCreated]
		gotOnce sync.Once
		done    = make(chan struct{})
	)
	sub, _ := Subscribe[orderCreated](ctx, c, "submeta.created",
		func(_ context.Context, m Msg[orderCreated]) error {
			mu.Lock()
			defer mu.Unlock()
			gotOnce.Do(func() {
				gotMsg = m
				close(done)
			})
			return nil
		},
		WithDurable("submeta-d1"),
	)
	t.Cleanup(func() { _ = sub.Drain() })

	pub := NewPublisher[orderCreated](c)
	_ = pub.Publish(ctx, "submeta.created", orderCreated{ID: "x", Amount: 1})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	mu.Lock()
	defer mu.Unlock()
	if gotMsg.Subject != "submeta.created" {
		t.Errorf("Subject = %q", gotMsg.Subject)
	}
	if gotMsg.Sequence == 0 {
		t.Errorf("Sequence = 0 (expected > 0)")
	}
	if gotMsg.Redeliveries != 0 {
		t.Errorf("Redeliveries = %d, want 0", gotMsg.Redeliveries)
	}
	if gotMsg.Raw() == nil {
		t.Errorf("Raw() returned nil")
	}
}

func TestSubscribe_MaxInFlightBound(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	const stream = "TEST_SUB_MIF"
	t.Cleanup(func() { _ = c.DeleteStream(ctx, stream) })
	_ = c.EnsureStream(ctx, StreamConfig{Name: stream, Subjects: []string{"submif.>"}})

	const maxInFlight = 3
	const total = 20

	var (
		mu       sync.Mutex
		inFlight int
		peak     int
		done     = make(chan struct{})
		count    int
	)
	sub, _ := Subscribe[orderCreated](ctx, c, "submif.created",
		func(_ context.Context, _ Msg[orderCreated]) error {
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			mu.Lock()
			inFlight--
			count++
			if count == total {
				close(done)
			}
			mu.Unlock()
			return nil
		},
		WithDurable("submif-d1"),
		WithMaxInFlight(maxInFlight),
	)
	t.Cleanup(func() { _ = sub.Drain() })

	pub := NewPublisher[orderCreated](c)
	for i := 0; i < total; i++ {
		_ = pub.Publish(ctx, "submif.created", orderCreated{ID: "x", Amount: i})
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > maxInFlight {
		t.Fatalf("peak inFlight = %d, exceeds MaxInFlight = %d", peak, maxInFlight)
	}
}
