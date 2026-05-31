package natsmap

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// orderBatch is the smoke test for the batched dispatch path:
// publish N messages, expect the batched handler to receive one or
// more slices summing to N items, with each item Ack'd by JetStream
// (no redelivery).
func TestBatched_DeliversSlice(t *testing.T) {
	c := newTestClient(t)
	if err := c.EnsureStream(context.Background(), natsclient.StreamConfig{
		Name: "BATCHTEST_DELIVERS", Subjects: []string{"batchtest.deliver.>"},
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	yaml := []byte(`subscribers:
  - name: batch_receiver
    subject: batchtest.deliver.orders
    batch_size: 3
    batch_interval: 100ms
publishers:
  - name: orders_out
    subject: batchtest.deliver.orders
`)
	eng := New()
	if err := eng.LoadBytes(yaml); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	var (
		mu      sync.Mutex
		batches [][]order
	)
	RegisterBatchedHandler[order](eng, "batch_receiver",
		func(ctx context.Context, batch []natsclient.Msg[order]) error {
			mu.Lock()
			items := make([]order, len(batch))
			for i, m := range batch {
				items[i] = m.Data
			}
			batches = append(batches, items)
			mu.Unlock()
			return nil
		})
	RegisterPublisher[order](eng, "orders_out")

	rt, err := eng.Build(context.Background(), c)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = rt.Drain() })

	for i := range 5 {
		if err := Publish[order](context.Background(), rt, "orders_out",
			order{ID: string(rune('a' + i))}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		total := 0
		for _, b := range batches {
			total += len(b)
		}
		mu.Unlock()
		if total >= 5 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	total := 0
	for _, b := range batches {
		total += len(b)
	}
	if total != 5 {
		t.Errorf("total items across batches = %d, want 5; batches=%v", total, batches)
	}
	// At least the first batch should hit the size cap of 3 (or land
	// via the interval if the publish-rate is slow).
	if len(batches) == 0 {
		t.Fatal("no batches dispatched")
	}
}

// TestBatched_NakRedeliversWholeBatch verifies the all-or-nothing
// ack semantics: when the handler returns an error every msg gets
// Nak'd, and JetStream redelivers the same batch on the next fetch.
func TestBatched_NakRedeliversWholeBatch(t *testing.T) {
	c := newTestClient(t)
	if err := c.EnsureStream(context.Background(), natsclient.StreamConfig{
		Name: "BATCHTEST_NAK", Subjects: []string{"batchtest.nak.>"},
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	yaml := []byte(`subscribers:
  - name: nak_receiver
    subject: batchtest.nak.orders
    batch_size: 2
    batch_interval: 100ms
    ack_wait: 500ms
publishers:
  - name: orders_out
    subject: batchtest.nak.orders
`)
	eng := New()
	if err := eng.LoadBytes(yaml); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	var (
		mu              sync.Mutex
		invocationCount int
	)
	boom := errors.New("forced-error")
	RegisterBatchedHandler[order](eng, "nak_receiver",
		func(ctx context.Context, batch []natsclient.Msg[order]) error {
			mu.Lock()
			invocationCount++
			n := invocationCount
			mu.Unlock()
			// First invocation: Nak (return err). Subsequent
			// invocations: Ack (return nil) so the test can exit
			// cleanly.
			if n == 1 {
				return boom
			}
			return nil
		})
	RegisterPublisher[order](eng, "orders_out")

	rt, err := eng.Build(context.Background(), c)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = rt.Drain() })

	for i := range 2 {
		if err := Publish[order](context.Background(), rt, "orders_out",
			order{ID: string(rune('a' + i))}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := invocationCount
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if invocationCount < 2 {
		t.Errorf("handler invocation count = %d, want >= 2 (Nak should have triggered redelivery)", invocationCount)
	}
}

// TestBatched_RegularHandlerAgainstBatchedSubscriber fails Build
// with the documented Code so misregistrations surface immediately.
func TestBatched_RegularHandlerAgainstBatchedSubscriber(t *testing.T) {
	c := newTestClient(t)
	if err := c.EnsureStream(context.Background(), natsclient.StreamConfig{
		Name: "BATCHTEST_MISMATCH", Subjects: []string{"batchtest.mismatch.>"},
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	yaml := []byte(`subscribers:
  - name: mismatch
    subject: batchtest.mismatch.x
    batch_size: 10
`)
	eng := New()
	if err := eng.LoadBytes(yaml); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	// Wrong: regular handler against a batched subscriber.
	RegisterHandler[order](eng, "mismatch",
		func(ctx context.Context, m natsclient.Msg[order]) error { return nil })

	_, err := eng.Build(context.Background(), c)
	if err == nil {
		t.Fatal("expected mode-mismatch error at Build")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeBatchHandlerRequired {
		t.Errorf("err = %v, want CodeBatchHandlerRequired", err)
	}
}

// TestBatched_BatchedHandlerAgainstRegularSubscriber is the inverse
// — RegisterBatchedHandler without batch_size in YAML.
func TestBatched_BatchedHandlerAgainstRegularSubscriber(t *testing.T) {
	c := newTestClient(t)
	if err := c.EnsureStream(context.Background(), natsclient.StreamConfig{
		Name: "BATCHTEST_INV_MISMATCH", Subjects: []string{"batchtest.invmismatch.>"},
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	yaml := []byte(`subscribers:
  - name: inv_mismatch
    subject: batchtest.invmismatch.x
`)
	eng := New()
	if err := eng.LoadBytes(yaml); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	RegisterBatchedHandler[order](eng, "inv_mismatch",
		func(ctx context.Context, batch []natsclient.Msg[order]) error { return nil })

	_, err := eng.Build(context.Background(), c)
	if err == nil {
		t.Fatal("expected mode-mismatch error at Build")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeRegularHandlerRequired {
		t.Errorf("err = %v, want CodeRegularHandlerRequired", err)
	}
}

// Sanity: a successful batched deliver does NOT redeliver.
func TestBatched_AckPreventsRedelivery(t *testing.T) {
	c := newTestClient(t)
	if err := c.EnsureStream(context.Background(), natsclient.StreamConfig{
		Name: "BATCHTEST_ACK", Subjects: []string{"batchtest.ack.>"},
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	yaml := []byte(`subscribers:
  - name: ack_receiver
    subject: batchtest.ack.x
    batch_size: 1
    batch_interval: 50ms
    ack_wait: 300ms
publishers:
  - name: x_out
    subject: batchtest.ack.x
`)
	eng := New()
	if err := eng.LoadBytes(yaml); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	var calls atomic.Int64
	RegisterBatchedHandler[order](eng, "ack_receiver",
		func(ctx context.Context, batch []natsclient.Msg[order]) error {
			calls.Add(int64(len(batch)))
			return nil
		})
	RegisterPublisher[order](eng, "x_out")

	rt, err := eng.Build(context.Background(), c)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = rt.Drain() })

	if err := Publish[order](context.Background(), rt, "x_out", order{ID: "z"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Wait through one ack_wait window. If Ack wasn't honoured,
	// JetStream would redeliver and bump calls above 1.
	time.Sleep(700 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want exactly 1 (Ack should prevent redelivery)", got)
	}
}
