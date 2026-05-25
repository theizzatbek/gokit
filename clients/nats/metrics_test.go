package natsclient

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetrics_PublishAndHandlerCountersMove(t *testing.T) {
	if testing.Short() || testURL == "" {
		t.Skip("integration")
	}
	reg := prometheus.NewRegistry()
	c, err := Connect(context.Background(), Config{URL: testURL, Name: "metrics-test"},
		WithMetrics(reg))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(c.Close)

	ctx := context.Background()
	const stream = "TEST_METRICS"
	t.Cleanup(func() { _ = c.DeleteStream(ctx, stream) })
	_ = c.EnsureStream(ctx, StreamConfig{Name: stream, Subjects: []string{"metr.>"}})

	done := make(chan struct{}, 1)
	sub, _ := Subscribe[orderCreated](ctx, c, "metr.x",
		func(_ context.Context, _ Msg[orderCreated]) error {
			done <- struct{}{}
			return nil
		},
		WithDurable("metr-d1"),
	)
	t.Cleanup(func() { _ = sub.Drain() })

	pub := NewPublisher[orderCreated](c)
	_ = pub.Publish(ctx, "metr.x", orderCreated{ID: "x", Amount: 1})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}

	pubCount := testutil.CollectAndCount(c.metrics.publishTotal)
	if pubCount == 0 {
		t.Errorf("publishTotal samples = 0, want > 0")
	}
	handlerCount := testutil.CollectAndCount(c.metrics.handlerTotal)
	if handlerCount == 0 {
		t.Errorf("handlerTotal samples = 0, want > 0")
	}
}
