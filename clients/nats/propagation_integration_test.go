package natsclient

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// TestTraceContext_EndToEnd_Integration verifies the full publish→subscribe
// round-trip: a publish-side span's TraceID survives the wire and is
// available on the consumer side via ExtractTraceContext (auto-applied
// in dispatchRaw).
func TestTraceContext_EndToEnd_Integration(t *testing.T) {
	if testing.Short() || testURL == "" {
		t.Skip("integration test — Docker required")
	}
	installPropagator(t)
	tp := installTracerProvider(t)

	c := newTestClient(t)
	if err := c.EnsureStream(context.Background(), StreamConfig{
		Name: "TRACE_PROP", Subjects: []string{"trace.>"}, MaxAge: time.Minute, Storage: StorageMemory,
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	publishCtx, publishSpan := tp.Tracer("test").Start(context.Background(), "publish-side")
	defer publishSpan.End()
	wantTraceID := publishSpan.SpanContext().TraceID()

	// Subscribe FIRST so the upcoming publish lands as a deliverable.
	type handlerResult struct {
		gotTraceID trace.TraceID
	}
	results := make(chan handlerResult, 1)
	var once sync.Once
	sub, err := SubscribeRaw(context.Background(), c, "trace.test",
		RawHandler(func(ctx context.Context, m *RawMsg) error {
			once.Do(func() {
				results <- handlerResult{
					gotTraceID: trace.SpanContextFromContext(ctx).TraceID(),
				}
			})
			return nil
		}))
	if err != nil {
		t.Fatalf("SubscribeRaw: %v", err)
	}
	t.Cleanup(func() { _ = sub.Drain() })

	if err := PublishRaw(publishCtx, c, "trace.test", []byte(`{"ok":true}`), nil); err != nil {
		t.Fatalf("PublishRaw: %v", err)
	}

	select {
	case res := <-results:
		if res.gotTraceID != wantTraceID {
			t.Errorf("consumer-side TraceID = %s, want %s — propagation broke",
				res.gotTraceID, wantTraceID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler never fired")
	}
}

// TestTraceContext_OutboxReplay_Integration simulates the outbox path:
// (1) capture a TraceContext into headers as Enqueue would, (2) later
// publish via PublishRaw with those headers + a different ctx (no
// span), (3) consumer should still see the ORIGINAL TraceID — proving
// "preserve existing traceparent" survives the long-lived outbox row.
func TestTraceContext_OutboxReplay_Integration(t *testing.T) {
	if testing.Short() || testURL == "" {
		t.Skip("integration test — Docker required")
	}
	installPropagator(t)
	tp := installTracerProvider(t)

	c := newTestClient(t)
	if err := c.EnsureStream(context.Background(), StreamConfig{
		Name: "TRACE_OUTBOX", Subjects: []string{"outbox.>"}, MaxAge: time.Minute, Storage: StorageMemory,
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	// Step 1: Origin ctx has a span → snapshot headers.
	enqueueCtx, enqueueSpan := tp.Tracer("test").Start(context.Background(), "enqueue")
	originalHeaders := InjectTraceContext(enqueueCtx, nil)
	wantTraceID := enqueueSpan.SpanContext().TraceID()
	enqueueSpan.End()

	// Step 2: Subscribe.
	results := make(chan trace.TraceID, 1)
	var once sync.Once
	sub, err := SubscribeRaw(context.Background(), c, "outbox.replay",
		RawHandler(func(ctx context.Context, m *RawMsg) error {
			once.Do(func() {
				results <- trace.SpanContextFromContext(ctx).TraceID()
			})
			return nil
		}))
	if err != nil {
		t.Fatalf("SubscribeRaw: %v", err)
	}
	t.Cleanup(func() { _ = sub.Drain() })

	// Step 3: Publish with a DIFFERENT ctx (no active span) plus the
	// snapshotted headers. The "preserve existing traceparent" branch
	// in InjectTraceContext should leave the original IDs intact.
	workerCtx := context.Background()
	if err := PublishRaw(workerCtx, c, "outbox.replay", []byte(`{"replay":true}`), originalHeaders); err != nil {
		t.Fatalf("PublishRaw: %v", err)
	}

	select {
	case got := <-results:
		if got != wantTraceID {
			t.Errorf("consumer TraceID = %s, want %s (outbox should preserve origin trace)",
				got, wantTraceID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler never fired")
	}
	_ = otel.GetTracerProvider() // keep import path used even when otel global isn't touched
}
