package natsclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
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

func TestSubscribe_HandlerErrorRetriesWithBackoff(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	const stream = "TEST_SUB_RETRY"
	t.Cleanup(func() { _ = c.DeleteStream(ctx, stream) })
	_ = c.EnsureStream(ctx, StreamConfig{Name: stream, Subjects: []string{"subretry.>"}})

	var (
		mu           sync.Mutex
		redeliveries []int
		seen         = make(chan struct{}, 4)
	)
	sub, _ := Subscribe[orderCreated](ctx, c, "subretry.created",
		func(_ context.Context, m Msg[orderCreated]) error {
			mu.Lock()
			redeliveries = append(redeliveries, m.Redeliveries)
			mu.Unlock()
			select {
			case seen <- struct{}{}:
			default:
			}
			return errors.New("force retry")
		},
		WithDurable("subretry-d1"),
		WithMaxDeliver(3),
		WithBackoff(func(_ int) time.Duration { return 50 * time.Millisecond }),
	)
	t.Cleanup(func() { _ = sub.Drain() })

	pub := NewPublisher[orderCreated](c)
	_ = pub.Publish(ctx, "subretry.created", orderCreated{ID: "x", Amount: 1})

	deadline := time.After(5 * time.Second)
	for got := 0; got < 2; {
		select {
		case <-seen:
			got++
		case <-deadline:
			t.Fatalf("only saw %d deliveries", got)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if redeliveries[0] != 0 || redeliveries[1] < 1 {
		t.Fatalf("redeliveries = %v, want [0, ≥1, ...]", redeliveries)
	}
}

func TestSubscribe_DecodeFailureTerms(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	const stream = "TEST_SUB_DECODE"
	t.Cleanup(func() { _ = c.DeleteStream(ctx, stream) })
	_ = c.EnsureStream(ctx, StreamConfig{Name: stream, Subjects: []string{"subdec.>"}})

	called := make(chan struct{}, 1)
	sub, _ := Subscribe[orderCreated](ctx, c, "subdec.created",
		func(_ context.Context, _ Msg[orderCreated]) error {
			called <- struct{}{}
			return nil
		},
		WithDurable("subdec-d1"),
		WithMaxDeliver(3),
	)
	t.Cleanup(func() { _ = sub.Drain() })

	// Publish raw garbage that won't decode into orderCreated.
	_, _ = c.JetStream().Publish("subdec.created", []byte("not-json"))

	// Handler must NOT be called.
	select {
	case <-called:
		t.Fatal("handler was called for undecodeable payload")
	case <-time.After(800 * time.Millisecond):
		// good — Term'd
	}

	// Stream sanity check — informational, not strict.
	si, err := c.JetStream().StreamInfo(stream)
	if err != nil {
		t.Fatalf("StreamInfo: %v", err)
	}
	_ = si
}

func TestSubscribe_OptionsCompileAndApply(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	const stream = "TEST_SUB_OPTS"
	t.Cleanup(func() { _ = c.DeleteStream(ctx, stream) })
	_ = c.EnsureStream(ctx, StreamConfig{Name: stream, Subjects: []string{"subopts.>"}})

	sub, err := Subscribe[orderCreated](ctx, c, "subopts.x",
		func(_ context.Context, _ Msg[orderCreated]) error { return nil },
		WithDurable("subopts-d1"),
		WithStartFrom(StartNew()),
		WithQueueGroup("g1"),
	)
	if err != nil {
		t.Fatalf("Subscribe with options: %v", err)
	}
	_ = sub.Drain()
}

func TestSubscribe_LogsConsumerDrift(t *testing.T) {
	if testing.Short() || testURL == "" {
		t.Skip("integration")
	}
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c, err := Connect(context.Background(), Config{URL: testURL, Name: "drift"}, WithLogger(logger))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(c.Close)

	ctx := context.Background()
	const stream = "TEST_SUB_DRIFT"
	t.Cleanup(func() { _ = c.DeleteStream(ctx, stream) })
	_ = c.EnsureStream(ctx, StreamConfig{Name: stream, Subjects: []string{"subdrift.>"}})

	// Pre-create the durable consumer with AckWait=1s so it survives
	// independent of any subscription's lifecycle.
	if _, err := c.js.AddConsumer(stream, &nats.ConsumerConfig{
		Durable:        "subdrift-d1",
		DeliverSubject: "deliver.subdrift",
		DeliverPolicy:  nats.DeliverNewPolicy,
		AckPolicy:      nats.AckExplicitPolicy,
		AckWait:        1 * time.Second,
		MaxDeliver:     5,
		FilterSubject:  "subdrift.x",
	}); err != nil {
		t.Fatalf("AddConsumer: %v", err)
	}

	// Subscribe with same durable but mismatched AckWait → kit should log
	// the drift warning. NATS itself will then reject the Subscribe (this is
	// expected — kit's Warn is advisory and surfaces the diff before the
	// cryptic server-side error).
	sub, _ := Subscribe[orderCreated](ctx, c, "subdrift.x",
		func(_ context.Context, _ Msg[orderCreated]) error { return nil },
		WithDurable("subdrift-d1"),
		WithAckWait(10*time.Second),
	)
	if sub != nil {
		_ = sub.Drain()
	}

	if !strings.Contains(logBuf.String(), "consumer config drift") {
		t.Fatalf("drift warning not logged; log:\n%s", logBuf.String())
	}
}

func TestSubscribeRaw_ErrPoisonTermsMessage(t *testing.T) {
	c := newTestClient(t)
	if err := c.EnsureStream(context.Background(), StreamConfig{
		Name: "RAWTEST", Subjects: []string{"rawtest.>"},
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	calls := make(chan int, 8)
	sub, err := SubscribeRaw(context.Background(), c, "rawtest.poison",
		func(ctx context.Context, m *RawMsg) error {
			calls <- m.Redeliveries
			return fmt.Errorf("decode: %w: bad", ErrPoison)
		},
		WithMaxDeliver(5),
		WithAckWait(500*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("SubscribeRaw: %v", err)
	}
	defer sub.Drain()

	pub := NewPublisher[[]byte](c)
	if err := pub.Publish(context.Background(), "rawtest.poison", []byte("x")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("handler not called")
	}

	// ErrPoison should TERM the message — no redelivery within the next 2s.
	select {
	case got := <-calls:
		t.Fatalf("poison message redelivered: redeliveries=%d", got)
	case <-time.After(2 * time.Second):
	}
}

func TestSubscribeRaw_NormalErrorNaks(t *testing.T) {
	c := newTestClient(t)
	if err := c.EnsureStream(context.Background(), StreamConfig{
		Name: "RAWTEST2", Subjects: []string{"rawtest2.>"},
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	calls := make(chan int, 8)
	sub, err := SubscribeRaw(context.Background(), c, "rawtest2.nak",
		func(ctx context.Context, m *RawMsg) error {
			calls <- m.Redeliveries
			if m.Redeliveries < 1 {
				return errors.New("transient")
			}
			return nil
		},
		WithMaxDeliver(5),
		WithBackoff(func(int) time.Duration { return 100 * time.Millisecond }),
	)
	if err != nil {
		t.Fatalf("SubscribeRaw: %v", err)
	}
	defer sub.Drain()

	pub := NewPublisher[[]byte](c)
	if err := pub.Publish(context.Background(), "rawtest2.nak", []byte("x")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	gotZero, gotOne := false, false
	deadline := time.After(3 * time.Second)
	for !gotZero || !gotOne {
		select {
		case n := <-calls:
			if n == 0 {
				gotZero = true
			}
			if n == 1 {
				gotOne = true
			}
		case <-deadline:
			t.Fatalf("expected redelivery; gotZero=%v gotOne=%v", gotZero, gotOne)
		}
	}
}
