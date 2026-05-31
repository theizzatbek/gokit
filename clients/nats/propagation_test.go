package natsclient

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// installPropagator swaps the global TextMapPropagator with the W3C
// TraceContext propagator + restores it on test cleanup. The kit
// doesn't install a propagator at package import time, so explicit
// install is required to exercise inject/extract.
func installPropagator(t *testing.T) {
	t.Helper()
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })
}

// installTracerProvider swaps the global TracerProvider with one that
// records spans. Returns the recorder + restores on cleanup.
func installTracerProvider(t *testing.T) *sdktrace.TracerProvider {
	t.Helper()
	prev := otel.GetTracerProvider()
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return tp
}

func TestInjectTraceContext_NoPropagator_NoOp(t *testing.T) {
	// Without TextMapPropagator set globally, otel returns a no-op
	// propagator. Inject must NOT add anything to the carrier.
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

	h := map[string][]string{}
	InjectTraceContext(context.Background(), h)
	if _, present := h["traceparent"]; present {
		t.Errorf("traceparent should not be written without an active propagator; got %v", h)
	}
}

func TestInjectTraceContext_WritesTraceparent(t *testing.T) {
	installPropagator(t)
	tp := installTracerProvider(t)

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "publish")
	defer span.End()

	h := InjectTraceContext(ctx, nil)
	if got := h["traceparent"]; len(got) == 0 || got[0] == "" {
		t.Errorf("expected traceparent in headers, got %v", h)
	}
}

func TestInjectTraceContext_PreservesExistingTraceparent(t *testing.T) {
	// Outbox use case: headers already carry the originating trace.
	// Worker's later Inject with a different ctx must NOT overwrite.
	installPropagator(t)
	tp := installTracerProvider(t)

	tracer := tp.Tracer("test")
	originCtx, originSpan := tracer.Start(context.Background(), "origin")
	originHeaders := InjectTraceContext(originCtx, nil)
	originSpan.End()
	originalTP := originHeaders["traceparent"][0]

	// Different ctx (different span) — must not overwrite.
	workerCtx, workerSpan := tracer.Start(context.Background(), "worker")
	defer workerSpan.End()

	preserved := InjectTraceContext(workerCtx, originHeaders)
	if got := preserved["traceparent"][0]; got != originalTP {
		t.Errorf("traceparent should be preserved; got %q, original %q", got, originalTP)
	}
}

func TestExtractTraceContext_RestoresRemoteSpanContext(t *testing.T) {
	installPropagator(t)
	tp := installTracerProvider(t)

	tracer := tp.Tracer("test")
	publishCtx, publishSpan := tracer.Start(context.Background(), "publish")
	headers := InjectTraceContext(publishCtx, nil)
	wantTraceID := publishSpan.SpanContext().TraceID()
	publishSpan.End()

	// Imagine a wire crossing here — start a fresh ctx and extract.
	consumeCtx := ExtractTraceContext(context.Background(), headers)
	got := trace.SpanContextFromContext(consumeCtx).TraceID()
	if got != wantTraceID {
		t.Errorf("extracted TraceID = %s, want %s — extract did not restore the upstream trace",
			got, wantTraceID)
	}
}

func TestHeaderCarrier_ContractSurface(t *testing.T) {
	c := headerCarrier{"a": {"1"}, "b": {"2", "extra"}}

	if got := c.Get("a"); got != "1" {
		t.Errorf("Get(a) = %q, want 1", got)
	}
	if got := c.Get("b"); got != "2" {
		t.Errorf("Get(b) = %q, want 2 (first value only)", got)
	}
	if got := c.Get("missing"); got != "" {
		t.Errorf("Get(missing) = %q, want empty", got)
	}

	c.Set("c", "v")
	if got := c["c"]; len(got) != 1 || got[0] != "v" {
		t.Errorf("Set/c = %v, want [v]", got)
	}

	keys := map[string]bool{}
	for _, k := range c.Keys() {
		keys[k] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !keys[want] {
			t.Errorf("Keys() missing %q; got %v", want, keys)
		}
	}
}
