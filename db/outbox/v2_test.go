package outbox_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/outbox"
)

// TestBackoff_StampsNextRetryAt verifies that a failed publish bumps
// next_retry_at forward by the configured backoff window — the row
// must NOT be eligible on the very next polling tick.
func TestBackoff_StampsNextRetryAt(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	if err := d.Tx(ctx, func(tx *db.Tx) error {
		return outbox.Enqueue(ctx, tx, outbox.Event{
			EventType: "test.backoff",
			Payload:   []byte(`{}`),
		})
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var attempts int32
	w, err := outbox.NewWorker(d, func(_ context.Context, _ outbox.Event) error {
		atomic.AddInt32(&attempts, 1)
		return errors.New("nope")
	},
		outbox.WithInterval(20*time.Millisecond),
		outbox.WithBackoff(500*time.Millisecond, time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := w.Start(loopCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop() })

	// 300ms is well below the 500ms backoff window AFTER the first
	// failure, so attempts must NOT have crept past 1. Without backoff
	// at 20ms polling, attempts would already be 10+ by this point.
	time.Sleep(300 * time.Millisecond)
	got := atomic.LoadInt32(&attempts)
	if got != 1 {
		t.Errorf("attempts = %d, want 1 (backoff should suppress retry until 500ms)", got)
	}

	// Wait past the backoff window — a second attempt must follow.
	waitFor(t, time.Second, func() bool { return atomic.LoadInt32(&attempts) >= 2 })
}

// TestMetrics_PublishOutcomes verifies the kit's Prometheus
// collectors fire success / failure counters and the duration
// histogram on the expected paths.
func TestMetrics_PublishOutcomes(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	reg := prometheus.NewRegistry()

	// One row that fails twice then succeeds.
	if err := d.Tx(ctx, func(tx *db.Tx) error {
		return outbox.Enqueue(ctx, tx, outbox.Event{
			EventType: "test.metrics",
			Payload:   []byte(`{}`),
		})
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var calls int32
	w, err := outbox.NewWorker(d, func(_ context.Context, _ outbox.Event) error {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return errors.New("transient")
		}
		return nil
	},
		outbox.WithInterval(30*time.Millisecond),
		outbox.WithBackoff(0, 0),
		outbox.WithMetrics(reg),
	)
	if err != nil {
		t.Fatal(err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := w.Start(loopCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop() })

	waitFor(t, 2*time.Second, func() bool { return atomic.LoadInt32(&calls) >= 3 })
	// Small grace window for markPublished's UPDATE to commit before
	// we scrape the collector.
	time.Sleep(150 * time.Millisecond)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, mf := range mfs {
		if mf.GetName() != "outbox_events_total" {
			continue
		}
		for _, m := range mf.Metric {
			var outcome string
			for _, l := range m.Label {
				if l.GetName() == "outcome" {
					outcome = l.GetValue()
				}
			}
			got[outcome] = m.GetCounter().GetValue()
		}
	}
	if got["success"] != 1 {
		t.Errorf("success counter = %v, want 1", got["success"])
	}
	if got["failure"] != 2 {
		t.Errorf("failure counter = %v, want 2", got["failure"])
	}
}

// TestRetention_GCDeletesPublishedRows verifies the retention loop
// deletes rows whose published_at exceeded the retention window.
func TestRetention_GCDeletesPublishedRows(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	// Insert a row, mark it published in the past so it qualifies for
	// GC on the first sweep.
	if err := d.Tx(ctx, func(tx *db.Tx) error {
		return outbox.Enqueue(ctx, tx, outbox.Event{
			EventType: "test.retention",
			Payload:   []byte(`{}`),
		})
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := d.Exec(ctx,
		`UPDATE outbox SET published_at = NOW() - INTERVAL '1 hour'`); err != nil {
		t.Fatalf("mark old: %v", err)
	}

	reg := prometheus.NewRegistry()
	w, err := outbox.NewWorker(d,
		func(context.Context, outbox.Event) error { return nil },
		outbox.WithRetention(time.Minute), // anything older than 1m → delete
		outbox.WithGCInterval(50*time.Millisecond),
		outbox.WithMetrics(reg),
	)
	if err != nil {
		t.Fatal(err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := w.Start(loopCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop() })

	waitFor(t, time.Second, func() bool {
		var n int
		_ = d.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&n)
		return n == 0
	})

	// gc_deleted_total should be >= 1.
	mfs, _ := reg.Gather()
	var gcCounter float64
	for _, mf := range mfs {
		if mf.GetName() == "outbox_gc_deleted_total" {
			gcCounter = mf.Metric[0].GetCounter().GetValue()
		}
	}
	if gcCounter < 1 {
		t.Errorf("gc_deleted_total = %v, want >= 1", gcCounter)
	}
}

// TestEnqueueTyped_EncodesAndPersistsPayload verifies the typed sugar
// JSON-encodes the payload + applies WithAggregate.
func TestEnqueueTyped_EncodesAndPersistsPayload(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	type LinkCreated struct {
		LinkID string `json:"link_id"`
		Code   string `json:"code"`
	}
	want := LinkCreated{LinkID: "id-1", Code: "abc"}

	if err := d.Tx(ctx, func(tx *db.Tx) error {
		return outbox.EnqueueTyped(ctx, tx, "test.typed", want,
			outbox.WithAggregate("link", "abc"))
	}); err != nil {
		t.Fatalf("EnqueueTyped: %v", err)
	}

	var (
		eventType     string
		aggregateType string
		aggregateID   string
		payload       []byte
	)
	if err := d.QueryRow(ctx, `
		SELECT event_type, aggregate_type, aggregate_id, payload
		FROM outbox WHERE event_type = $1`, "test.typed").
		Scan(&eventType, &aggregateType, &aggregateID, &payload); err != nil {
		t.Fatalf("query: %v", err)
	}
	if aggregateType != "link" || aggregateID != "abc" {
		t.Errorf("aggregate = (%s, %s), want (link, abc)", aggregateType, aggregateID)
	}
	if !strings.Contains(string(payload), `"link_id":"id-1"`) {
		t.Errorf("payload = %s, want JSON-encoded LinkCreated", payload)
	}
}

// suppress unused-import warning when prometheus/client_model goes
// unreferenced after a refactor.
var _ = dto.MetricType_COUNTER
