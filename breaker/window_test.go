package breaker

import (
	"testing"
	"time"
)

func TestWindow_RecordTotals(t *testing.T) {
	t.Parallel()
	w := newWindow(10*time.Second, 10) // 1s buckets
	start := time.Unix(1_700_000_000, 0)

	w.record(start, true)
	w.record(start, false)
	w.record(start, false)

	reqs, fails := w.totals()
	if reqs != 3 {
		t.Errorf("reqs = %d, want 3", reqs)
	}
	if fails != 2 {
		t.Errorf("fails = %d, want 2", fails)
	}
}

func TestWindow_RollsOverTime(t *testing.T) {
	t.Parallel()
	w := newWindow(10*time.Second, 10) // 1s buckets
	start := time.Unix(1_700_000_000, 0)

	// Fill 5 buckets with 1 failure each.
	for i := 0; i < 5; i++ {
		w.record(start.Add(time.Duration(i)*time.Second), false)
	}
	reqs, fails := w.totals()
	if reqs != 5 || fails != 5 {
		t.Fatalf("after 5 buckets: reqs=%d fails=%d, want 5/5", reqs, fails)
	}

	// Jump 10 seconds — every bucket rolls out.
	w.record(start.Add(15*time.Second), true)
	reqs, fails = w.totals()
	if reqs != 1 || fails != 0 {
		t.Fatalf("after full rotation: reqs=%d fails=%d, want 1/0", reqs, fails)
	}
}

func TestWindow_PartialRollKeepsLiveBuckets(t *testing.T) {
	t.Parallel()
	w := newWindow(5*time.Second, 5) // 1s buckets
	start := time.Unix(1_700_000_000, 0)

	w.record(start, false)                    // bucket 0
	w.record(start.Add(1*time.Second), false) // bucket 1
	w.record(start.Add(2*time.Second), false) // bucket 2

	// At t=3s, bucket 0 (covering [0,1)) is no longer live (window
	// span is 5s, so buckets older than 5s ago drop). Buckets 1 and
	// 2 are still in the window.
	w.record(start.Add(3*time.Second), false)
	reqs, fails := w.totals()
	if reqs != 4 || fails != 4 {
		t.Errorf("after 4 records in span: reqs=%d fails=%d, want 4/4", reqs, fails)
	}

	// Now advance to t=8s. Records from t=0,1,2,3 are all >5s old →
	// every bucket should be cleared except the current one.
	w.record(start.Add(8*time.Second), true)
	reqs, fails = w.totals()
	if reqs != 1 || fails != 0 {
		t.Errorf("after 5s gap: reqs=%d fails=%d, want 1/0", reqs, fails)
	}
}

func TestWindow_Reset(t *testing.T) {
	t.Parallel()
	w := newWindow(10*time.Second, 10)
	start := time.Unix(1_700_000_000, 0)
	for i := 0; i < 5; i++ {
		w.record(start.Add(time.Duration(i)*time.Second), false)
	}
	w.reset()
	reqs, fails := w.totals()
	if reqs != 0 || fails != 0 {
		t.Errorf("after reset: reqs=%d fails=%d, want 0/0", reqs, fails)
	}
}
