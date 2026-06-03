package lock_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/lock"
)

// ── WithLogger ────────────────────────────────────────────────────

func TestWithLogger_LogsAcquireAndRelease(t *testing.T) {
	d := freshDB(t)
	var buf threadSafeBuf
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	name := fmt.Sprintf("test.logger.%d", time.Now().UnixNano())
	lk := lock.New(d, name, lock.WithLogger(logger))

	ok, release, err := lk.TryAcquire(context.Background())
	if err != nil || !ok {
		t.Fatalf("acquire: %v / %v", err, ok)
	}
	release()

	logged := buf.String()
	if !strings.Contains(logged, "lock: acquired") {
		t.Errorf("missing acquired log: %s", logged)
	}
	if !strings.Contains(logged, "lock: released") {
		t.Errorf("missing released log: %s", logged)
	}
	if !strings.Contains(logged, name) {
		t.Errorf("log missing lock name: %s", logged)
	}
}

func TestWithLogger_LogsContended(t *testing.T) {
	d := freshDB(t)
	var buf threadSafeBuf
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	name := fmt.Sprintf("test.contended.%d", time.Now().UnixNano())
	// Hold the lock on instance #1.
	lk1 := lock.New(d, name)
	ok1, release1, _ := lk1.TryAcquire(context.Background())
	if !ok1 {
		t.Fatal("first acquire failed")
	}
	defer release1()

	// Instance #2 with logger sees contention.
	lk2 := lock.New(d, name, lock.WithLogger(logger))
	ok2, _, err := lk2.TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok2 {
		t.Fatal("expected contended")
	}
	if !strings.Contains(buf.String(), "lock: contended") {
		t.Errorf("missing contended log: %s", buf.String())
	}
}

// ── WithMetrics ───────────────────────────────────────────────────

func TestWithMetrics_AcquiredOutcome(t *testing.T) {
	d := freshDB(t)
	reg := prometheus.NewRegistry()
	name := fmt.Sprintf("test.metrics.acq.%d", time.Now().UnixNano())
	lk := lock.New(d, name, lock.WithMetrics(reg))

	ok, release, err := lk.TryAcquire(context.Background())
	if err != nil || !ok {
		t.Fatal(err)
	}
	release()

	if got := getCounterValue(t, reg, "lock_acquires_total", "outcome", "acquired"); got != 1 {
		t.Errorf("acquired counter = %v, want 1", got)
	}
}

func TestWithMetrics_ContendedOutcome(t *testing.T) {
	d := freshDB(t)
	name := fmt.Sprintf("test.metrics.cont.%d", time.Now().UnixNano())

	// Holder (no metrics).
	holder := lock.New(d, name)
	_, release, err := holder.TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	// Observer (with metrics) sees the contention.
	reg := prometheus.NewRegistry()
	observer := lock.New(d, name, lock.WithMetrics(reg))
	ok, _, err := observer.TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected contended")
	}
	if got := getCounterValue(t, reg, "lock_acquires_total", "outcome", "contended"); got != 1 {
		t.Errorf("contended counter = %v, want 1", got)
	}
}

func TestWithMetrics_HoldDurationObserved(t *testing.T) {
	d := freshDB(t)
	reg := prometheus.NewRegistry()
	name := fmt.Sprintf("test.metrics.hold.%d", time.Now().UnixNano())
	lk := lock.New(d, name, lock.WithMetrics(reg))

	_, release, _ := lk.TryAcquire(context.Background())
	time.Sleep(20 * time.Millisecond)
	release()

	count := getHistogramSampleCount(t, reg, "lock_hold_duration_seconds")
	if count != 1 {
		t.Errorf("histogram sample count = %d, want 1", count)
	}
}

// ── TryAcquireXact ────────────────────────────────────────────────

func TestTryAcquireXact_HappyPath(t *testing.T) {
	d := freshDB(t)
	name := fmt.Sprintf("test.xact.happy.%d", time.Now().UnixNano())
	lk := lock.New(d, name)

	var acquired bool
	err := d.Tx(context.Background(), func(tx *db.Tx) error {
		ok, err := lk.TryAcquireXact(context.Background(), tx)
		if err != nil {
			return err
		}
		acquired = ok
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("expected xact lock acquired")
	}

	// Tx ended → lock auto-released → re-acquire succeeds via session-level path.
	ok, release, err := lk.TryAcquire(context.Background())
	if err != nil || !ok {
		t.Errorf("expected lock free after tx end: ok=%v err=%v", ok, err)
		return
	}
	release()
}

func TestTryAcquireXact_ContendedReturnsFalse(t *testing.T) {
	d := freshDB(t)
	name := fmt.Sprintf("test.xact.contended.%d", time.Now().UnixNano())
	lk := lock.New(d, name)

	// Session-level holder.
	ok, release, err := lk.TryAcquire(context.Background())
	if err != nil || !ok {
		t.Fatalf("holder: %v %v", err, ok)
	}
	defer release()

	err = d.Tx(context.Background(), func(tx *db.Tx) error {
		got, err := lk.TryAcquireXact(context.Background(), tx)
		if err != nil {
			return err
		}
		if got {
			t.Error("expected contended (false)")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTryAcquireXact_AutoReleaseOnRollback(t *testing.T) {
	d := freshDB(t)
	name := fmt.Sprintf("test.xact.rollback.%d", time.Now().UnixNano())
	lk := lock.New(d, name)

	sentinel := fmt.Errorf("forced rollback")
	err := d.Tx(context.Background(), func(tx *db.Tx) error {
		ok, err := lk.TryAcquireXact(context.Background(), tx)
		if err != nil || !ok {
			t.Fatal("expected acquired inside tx")
		}
		return sentinel
	})
	if err == nil {
		t.Fatal("expected sentinel propagation")
	}

	// Even after rollback, the lock must be free.
	ok, release, err := lk.TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("lock still held after rolled-back tx — auto-release broken")
	} else {
		release()
	}
}

func TestTryAcquireXact_NilTxErrors(t *testing.T) {
	d := freshDB(t)
	lk := lock.New(d, "test.xact.niltx")
	_, err := lk.TryAcquireXact(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on nil tx")
	}
}

// ── IsHeld diagnostic ────────────────────────────────────────────

func TestIsHeld_FreeLock(t *testing.T) {
	d := freshDB(t)
	name := fmt.Sprintf("test.isheld.free.%d", time.Now().UnixNano())
	lk := lock.New(d, name)

	held, err := lk.IsHeld(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if held {
		t.Error("expected free")
	}
}

func TestIsHeld_HeldLock(t *testing.T) {
	d := freshDB(t)
	name := fmt.Sprintf("test.isheld.held.%d", time.Now().UnixNano())
	lk := lock.New(d, name)

	_, release, err := lk.TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	held, err := lk.IsHeld(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !held {
		t.Error("expected held=true")
	}
}

// ── helpers ───────────────────────────────────────────────────────

type threadSafeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *threadSafeBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *threadSafeBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func getCounterValue(t *testing.T, reg *prometheus.Registry, name, labelKey, labelValue string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			ok := false
			for _, l := range m.GetLabel() {
				if l.GetName() == labelKey && l.GetValue() == labelValue {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
			if m.Counter != nil {
				return m.Counter.GetValue()
			}
		}
	}
	return 0
}

func getHistogramSampleCount(t *testing.T, reg *prometheus.Registry, name string) uint64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var total uint64
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if m.Histogram != nil {
				total += m.Histogram.GetSampleCount()
			}
		}
	}
	return total
}

// keep dto import alive even when assertions don't use it directly
var _ = dto.MetricFamily{}
