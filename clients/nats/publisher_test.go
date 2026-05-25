package natsclient

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

type orderCreated struct {
	ID     string `json:"id"`
	Amount int    `json:"amount"`
}

func TestPublisher_CoreSubjectFireAndForget(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	got := make(chan orderCreated, 1)
	sub, err := c.Conn().Subscribe("core.test.publish", func(m *nats.Msg) {
		var v orderCreated
		_ = (JSONCodec{}).Unmarshal(m.Data, &v)
		got <- v
	})
	if err != nil {
		t.Fatalf("core subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	pub := NewPublisher[orderCreated](c)
	want := orderCreated{ID: "o-1", Amount: 42}
	if err := pub.Publish(ctx, "core.test.publish", want); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case v := <-got:
		if v != want {
			t.Fatalf("got %+v, want %+v", v, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for message")
	}
}

func TestPublisher_JetStreamPublishGetsAck(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	const stream = "TEST_PUB_JS"
	t.Cleanup(func() { _ = c.DeleteStream(ctx, stream) })
	if err := c.EnsureStream(ctx, StreamConfig{Name: stream, Subjects: []string{"pubjs.>"}}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	pub := NewPublisher[orderCreated](c)
	if err := pub.Publish(ctx, "pubjs.created", orderCreated{ID: "o-2", Amount: 5}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	si, err := c.JetStream().StreamInfo(stream)
	if err != nil {
		t.Fatalf("StreamInfo: %v", err)
	}
	if si.State.Msgs != 1 {
		t.Fatalf("stream msgs = %d, want 1", si.State.Msgs)
	}
}

func TestPublisher_ConcurrentSafe(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	const stream = "TEST_PUB_CONC"
	t.Cleanup(func() { _ = c.DeleteStream(ctx, stream) })
	_ = c.EnsureStream(ctx, StreamConfig{Name: stream, Subjects: []string{"pubconc.>"}})

	pub := NewPublisher[orderCreated](c)
	var wg sync.WaitGroup
	var pubErr error
	var pubErrMu sync.Mutex
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := pub.Publish(ctx, "pubconc.created", orderCreated{ID: "o", Amount: i}); err != nil {
				pubErrMu.Lock()
				pubErr = errors.Join(pubErr, err)
				pubErrMu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if pubErr != nil {
		t.Fatalf("concurrent publishes: %v", pubErr)
	}
	si, _ := c.JetStream().StreamInfo(stream)
	if si.State.Msgs != 20 {
		t.Fatalf("stream msgs = %d, want 20", si.State.Msgs)
	}
}
