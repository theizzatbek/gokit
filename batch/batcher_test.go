package batch_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/theizzatbek/gokit/batch"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// captureHandler records every batch passed to HandlerFn so tests
// can assert what was dispatched.
type captureHandler[T any] struct {
	mu      sync.Mutex
	batches [][]T
	retErr  error
}

func (c *captureHandler[T]) Fn(_ context.Context, batch []T) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]T, len(batch))
	copy(cp, batch)
	c.batches = append(c.batches, cp)
	return c.retErr
}

func (c *captureHandler[T]) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.batches)
}

func (c *captureHandler[T]) last() []T {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.batches) == 0 {
		return nil
	}
	return c.batches[len(c.batches)-1]
}

// syncBuffer is a goroutine-safe wrapper around bytes.Buffer for
// slog handler sinks shared across the flush goroutine and the
// asserting goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestNew_RequiresHandlerFn(t *testing.T) {
	_, err := batch.New[int](batch.Config[int]{BatchSize: 10})
	if err == nil {
		t.Fatal("expected error for nil HandlerFn")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != batch.CodeMissingHandlerFn {
		t.Errorf("err = %v, want CodeMissingHandlerFn", err)
	}
}

func TestNew_RequiresBatchSize(t *testing.T) {
	_, err := batch.New[int](batch.Config[int]{
		HandlerFn: func(context.Context, []int) error { return nil },
	})
	if err == nil {
		t.Fatal("expected error for zero BatchSize")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != batch.CodeInvalidBatchSize {
		t.Errorf("err = %v, want CodeInvalidBatchSize", err)
	}
}

func TestNew_JoinsAllMissingFields(t *testing.T) {
	_, err := batch.New[int](batch.Config[int]{})
	if err == nil {
		t.Fatal("expected error for empty config")
	}
	msg := err.Error()
	for _, want := range []string{
		batch.CodeMissingHandlerFn,
		batch.CodeInvalidBatchSize,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("joined err missing %q: %v", want, err)
		}
	}
}

func TestSubmit_HandlerReceivesSlice(t *testing.T) {
	cap := &captureHandler[int]{}
	b, _ := batch.New[int](batch.Config[int]{
		HandlerFn: cap.Fn,
		BatchSize: 10,
		Interval:  10 * time.Millisecond,
	})
	defer b.Close()

	for i := 1; i <= 3; i++ {
		b.Submit(i, nil)
	}
	if err := b.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	got := cap.last()
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("batch = %v, want [1 2 3] preserving submit order", got)
	}
}

func TestSubmit_AckFiresWithHandlerNil(t *testing.T) {
	cap := &captureHandler[int]{}
	b, _ := batch.New[int](batch.Config[int]{
		HandlerFn: cap.Fn,
		BatchSize: 10,
	})
	defer b.Close()

	var (
		mu       sync.Mutex
		gotErrs  []error
		expected = 3
	)
	for range expected {
		b.Submit(1, func(err error) {
			mu.Lock()
			gotErrs = append(gotErrs, err)
			mu.Unlock()
		})
	}
	_ = b.Flush(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(gotErrs) != expected {
		t.Fatalf("ack fired %d times, want %d", len(gotErrs), expected)
	}
	for i, e := range gotErrs {
		if e != nil {
			t.Errorf("ack[%d] = %v, want nil", i, e)
		}
	}
}

func TestSubmit_AckFiresWithHandlerErr(t *testing.T) {
	boom := errors.New("downstream-down")
	cap := &captureHandler[int]{retErr: boom}
	b, _ := batch.New[int](batch.Config[int]{
		HandlerFn: cap.Fn,
		BatchSize: 10,
	})
	defer b.Close()

	var (
		mu      sync.Mutex
		gotErrs []error
	)
	for range 4 {
		b.Submit(1, func(err error) {
			mu.Lock()
			gotErrs = append(gotErrs, err)
			mu.Unlock()
		})
	}
	_ = b.Flush(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(gotErrs) != 4 {
		t.Fatalf("ack count = %d, want 4", len(gotErrs))
	}
	for i, e := range gotErrs {
		if !errors.Is(e, boom) {
			t.Errorf("ack[%d] = %v, want %v (all-or-nothing)", i, e, boom)
		}
	}
}

func TestSubmit_NilAckIsSafe(t *testing.T) {
	cap := &captureHandler[int]{}
	b, _ := batch.New[int](batch.Config[int]{
		HandlerFn: cap.Fn,
		BatchSize: 10,
	})
	defer b.Close()

	// Mix nil and non-nil ack callbacks within one batch.
	var nonNilFired atomic.Int64
	b.Submit(1, nil)
	b.Submit(2, func(error) { nonNilFired.Add(1) })
	b.Submit(3, nil)
	b.Submit(4, func(error) { nonNilFired.Add(1) })

	_ = b.Flush(context.Background())

	if got := nonNilFired.Load(); got != 2 {
		t.Errorf("non-nil ack fired %d times, want 2", got)
	}
}

func TestIntervalTrigger(t *testing.T) {
	cap := &captureHandler[int]{}
	b, _ := batch.New[int](batch.Config[int]{
		HandlerFn: cap.Fn,
		BatchSize: 100,
		Interval:  5 * time.Millisecond,
	})
	defer b.Close()

	b.Submit(1, nil)
	time.Sleep(30 * time.Millisecond)

	if cap.calls() == 0 {
		t.Fatal("ticker did not fire flush within the interval window")
	}
}

func TestSizeTrigger(t *testing.T) {
	cap := &captureHandler[int]{}
	b, _ := batch.New[int](batch.Config[int]{
		HandlerFn: cap.Fn,
		BatchSize: 3,
		Interval:  10 * time.Second,
	})
	defer b.Close()

	b.Submit(1, nil)
	b.Submit(2, nil)
	b.Submit(3, nil)

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if cap.calls() >= 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if cap.calls() == 0 {
		t.Fatal("size trigger did not fire flush")
	}
}

func TestClose_FinalFlush(t *testing.T) {
	cap := &captureHandler[int]{}
	b, _ := batch.New[int](batch.Config[int]{
		HandlerFn: cap.Fn,
		BatchSize: 100,
		Interval:  10 * time.Second,
	})

	b.Submit(42, nil)
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if cap.calls() == 0 {
		t.Error("Close did not perform a final flush")
	}
	got := cap.last()
	if len(got) != 1 || got[0] != 42 {
		t.Errorf("final batch = %v, want [42]", got)
	}
}

func TestClose_Idempotent(t *testing.T) {
	b, _ := batch.New[int](batch.Config[int]{
		HandlerFn: func(context.Context, []int) error { return nil },
		BatchSize: 10,
	})
	if err := b.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestHandlerError_LoggedAndAcked(t *testing.T) {
	boom := errors.New("flush-fail")
	buf := &syncBuffer{}
	b, _ := batch.New[int](batch.Config[int]{
		HandlerFn: func(context.Context, []int) error { return boom },
		BatchSize: 100,
		Interval:  5 * time.Millisecond,
		Logger:    slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	defer b.Close()

	var gotErr atomic.Value
	b.Submit(1, func(err error) { gotErr.Store(err) })
	time.Sleep(30 * time.Millisecond)

	if !strings.Contains(buf.String(), "handler returned error") {
		t.Errorf("Warn log missing: %q", buf.String())
	}
	if v := gotErr.Load(); v == nil || !errors.Is(v.(error), boom) {
		t.Errorf("ack got %v, want %v", v, boom)
	}
}

func TestNilReceiverSafe(t *testing.T) {
	var b *batch.Batcher[int]
	b.Submit(1, func(error) { t.Error("ack on nil-receiver Submit fired") })
	if err := b.Close(); err != nil {
		t.Errorf("nil Close = %v, want nil", err)
	}
	if err := b.Flush(context.Background()); err != nil {
		t.Errorf("nil Flush = %v, want nil", err)
	}
}

func TestMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	cap := &captureHandler[int]{}
	b, _ := batch.New[int](batch.Config[int]{
		HandlerFn: cap.Fn,
		BatchSize: 100,
		Metrics:   reg,
	})
	defer b.Close()

	b.Submit(1, nil)
	b.Submit(2, nil)
	b.Submit(3, nil)
	_ = b.Flush(context.Background())

	if got := counterValue(t, reg, "batch_items_processed_total", nil); got != 3 {
		t.Errorf("items_processed = %v, want 3", got)
	}
	if got := counterValue(t, reg, "batch_handlers_total", map[string]string{"outcome": "success"}); got != 1 {
		t.Errorf("handlers_total{success} = %v, want 1", got)
	}
	if !histogramObserved(t, reg, "batch_handler_duration_seconds") {
		t.Error("handler_duration histogram has no observation")
	}
	if !histogramObserved(t, reg, "batch_batch_size") {
		t.Error("batch_size histogram has no observation")
	}
}

// --- shared helpers ---

func counterValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.Metric {
			if labelsMatch(m, labels) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func histogramObserved(t *testing.T, reg *prometheus.Registry, name string) bool {
	t.Helper()
	mfs, _ := reg.Gather()
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.Metric {
			if m.GetHistogram().GetSampleCount() > 0 {
				return true
			}
		}
	}
	return false
}

func labelsMatch(m *dto.Metric, want map[string]string) bool {
	if want == nil {
		return true
	}
	got := map[string]string{}
	for _, l := range m.GetLabel() {
		got[l.GetName()] = l.GetValue()
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
