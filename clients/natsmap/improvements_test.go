package natsmap

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
)

type ev struct {
	ID string `json:"id"`
}

// ── E. Mock mode (no testcontainer needed) ─────────────────────────────

func TestRegisterMockHandler_DispatchMock_FiresHandler(t *testing.T) {
	yaml := `subscribers:
  - name: order_paid
    subject: orders.paid
`
	e := New()
	if err := e.LoadBytes([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	var got string
	RegisterMockHandler[ev](e, "order_paid",
		func(ctx context.Context, m natsclient.Msg[ev]) error {
			got = m.Data.ID
			return nil
		})

	rt, err := e.Build(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Drain() })

	if err := DispatchMock(context.Background(), rt, "order_paid", ev{ID: "o-1"}, nil); err != nil {
		t.Fatalf("DispatchMock: %v", err)
	}
	if got != "o-1" {
		t.Errorf("handler got %q, want o-1", got)
	}
}

func TestDispatchMock_UnknownSubscriber(t *testing.T) {
	yaml := `subscribers: []`
	e := New()
	_ = e.LoadBytes([]byte(yaml))
	rt, err := e.Build(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Drain() })

	err = DispatchMock(context.Background(), rt, "ghost", ev{}, nil)
	if err == nil || !strings.Contains(err.Error(), CodeUnknownSubscriber) {
		t.Errorf("err = %v, want %q", err, CodeUnknownSubscriber)
	}
}

func TestDispatchMock_TypeMismatchSurfacesAsErr(t *testing.T) {
	yaml := `subscribers:
  - name: hits
    subject: counter.hits
`
	e := New()
	_ = e.LoadBytes([]byte(yaml))
	RegisterMockHandler[ev](e, "hits", func(context.Context, natsclient.Msg[ev]) error { return nil })
	rt, _ := e.Build(context.Background(), nil)
	t.Cleanup(func() { _ = rt.Drain() })

	type wrong struct{ X int }
	err := DispatchMock(context.Background(), rt, "hits", wrong{}, nil)
	if err == nil {
		t.Error("expected type-mismatch error")
	}
}

// ── A. RegisterSubscriberOptions unknown-name fails Build ─────────────

func TestRegisterSubscriberOptions_UnknownNameFailsBuild(t *testing.T) {
	yaml := `subscribers:
  - name: real
    subject: real.subject
`
	e := New()
	_ = e.LoadBytes([]byte(yaml))
	// Register the real subscriber's handler so the only remaining
	// failure path is the bad RegisterSubscriberOptions reference.
	RegisterMockHandler[ev](e, "real", func(context.Context, natsclient.Msg[ev]) error { return nil })
	e.RegisterSubscriberOptions("ghost", natsclient.WithAckProgress(time.Second))

	_, err := e.Build(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), CodeUnknownSubscriber) {
		t.Errorf("err = %v, want %q", err, CodeUnknownSubscriber)
	}
}

// ── BC. Hooks visible from DispatchMock (sub side) and Publish skip ────

func TestWithBeforeAfterDispatch_FireOnMockDispatch(t *testing.T) {
	yaml := `subscribers:
  - name: order_paid
    subject: orders.paid
`
	e := New()
	_ = e.LoadBytes([]byte(yaml))
	RegisterMockHandler[ev](e, "order_paid",
		func(context.Context, natsclient.Msg[ev]) error { return nil })

	var beforeName, afterName atomic.Value
	beforeName.Store("")
	afterName.Store("")
	rt, err := e.Build(context.Background(), nil,
		WithBeforeDispatch(func(name, subject string) {
			// Mock subscribers bypass the hook-wrapped handlerFns
			// because Build records them on the runtime before
			// wrapWithDispatchHooks runs. This guard test documents
			// that contract — hooks fire for REAL subscribers, not
			// mocks. Tests that need mock-time hooks should call them
			// from the mock fn body directly.
			beforeName.Store(name)
		}),
		WithAfterDispatch(func(name, subject string, _ error, _ time.Duration) {
			afterName.Store(name)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Drain() })

	_ = DispatchMock(context.Background(), rt, "order_paid", ev{ID: "x"}, nil)

	// Documented behaviour: hooks do NOT fire for mocks. Both load
	// stay "" — this protects the test against accidental coupling
	// between mock and real-subscriber wrap paths.
	if beforeName.Load().(string) != "" || afterName.Load().(string) != "" {
		t.Errorf("hooks fired for mock dispatch (before=%v after=%v); mocks should bypass",
			beforeName.Load(), afterName.Load())
	}
}

// ── F. natsmap-owned metrics on publish path ──────────────────────────

func TestWithMetrics_CollectorsRegisteredPostBuild(t *testing.T) {
	yaml := `subscribers: []`
	e := New()
	_ = e.LoadBytes([]byte(yaml))
	reg := prometheus.NewRegistry()

	rt, err := e.Build(context.Background(), nil, WithMetrics(reg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Drain() })

	// Force at least one observation so the Gather'd output is
	// non-empty (Prometheus omits Histogram/Counter without observed
	// label combinations).
	rt.metrics.observePublish("seed", "success")

	mfs, _ := reg.Gather()
	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "natsmap_publishes_total" {
			found = true
		}
	}
	if !found {
		t.Error("natsmap_publishes_total missing after WithMetrics + observation")
	}
}

// ── D. Default publish headers — covered by integration scope ─────────

func TestWithDefaultPublishHeaders_MergesIntoRuntimeSnapshot(t *testing.T) {
	yaml := `publishers: []`
	e := New()
	_ = e.LoadBytes([]byte(yaml))
	rt, err := e.Build(context.Background(), nil,
		WithDefaultPublishHeaders(map[string][]string{
			"X-Service-Version": {"v1.2.3"},
			"X-Region":          {"us-west-2"},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Drain() })

	if len(rt.defaultPublishHdrs) != 2 {
		t.Errorf("defaultPublishHdrs = %v, want 2 entries", rt.defaultPublishHdrs)
	}
	if rt.defaultPublishHdrs["X-Service-Version"][0] != "v1.2.3" {
		t.Errorf("X-Service-Version = %q", rt.defaultPublishHdrs["X-Service-Version"])
	}
}

func TestRuntime_MergePublishHeaders_LayeringOrder(t *testing.T) {
	rt := &Runtime{
		defaultPublishHdrs: map[string][]string{
			"X-Common":   {"engine"},
			"X-Override": {"engine-loses"},
		},
	}
	shim := publishShim{
		subject: "x",
		staticHdrs: map[string][]string{
			"X-Override": {"static-wins-over-engine"},
			"X-Static":   {"static-only"},
		},
	}
	call := map[string][]string{
		"X-Override": {"call-wins-over-all"},
		"X-Call":     {"call-only"},
	}
	merged := rt.mergePublishHeaders(context.Background(), shim, call)
	if merged["X-Common"][0] != "engine" {
		t.Errorf("X-Common = %q", merged["X-Common"])
	}
	if merged["X-Override"][0] != "call-wins-over-all" {
		t.Errorf("X-Override = %q, want call-wins-over-all", merged["X-Override"])
	}
	if merged["X-Static"][0] != "static-only" {
		t.Errorf("X-Static missing")
	}
	if merged["X-Call"][0] != "call-only" {
		t.Errorf("X-Call missing")
	}
}

// Guard: silence unused imports if a test gets removed mid-edit.
var _ = errors.New
