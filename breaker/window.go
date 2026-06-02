package breaker

import "time"

// bucket holds the per-bucket counters of the rolling window.
type bucket struct {
	startedAt time.Time
	requests  int
	failures  int
}

// window is a fixed-size ring of buckets covering WindowDuration.
// roll(now) advances the head, zeroing any buckets that have aged out;
// record(now, success) increments the current bucket; totals() sums
// every live bucket.
//
// All access is single-threaded under the parent Breaker's mutex; the
// window itself takes no locks.
type window struct {
	buckets    []bucket
	head       int           // index of the current (newest) bucket
	bucketSpan time.Duration // WindowDuration / WindowSize
}

func newWindow(total time.Duration, size int) *window {
	return &window{
		buckets:    make([]bucket, size),
		bucketSpan: total / time.Duration(size),
	}
}

// reset clears every bucket. Called on transition back to closed.
func (w *window) reset() {
	for i := range w.buckets {
		w.buckets[i] = bucket{}
	}
	w.head = 0
}

// roll advances the head if now has crossed into a new bucket span.
// Buckets that are no longer in the window are zeroed.
func (w *window) roll(now time.Time) {
	// First-ever record: stamp the head and return.
	if w.buckets[w.head].startedAt.IsZero() {
		w.buckets[w.head].startedAt = now
		return
	}
	elapsed := now.Sub(w.buckets[w.head].startedAt)
	if elapsed < w.bucketSpan {
		return
	}
	// Number of bucket boundaries we crossed. Cap at len(buckets) so
	// long idle periods do not loop redundantly — every bucket is
	// zeroed anyway after a full rotation.
	steps := int(elapsed / w.bucketSpan)
	if steps > len(w.buckets) {
		steps = len(w.buckets)
	}
	for i := 0; i < steps; i++ {
		w.head = (w.head + 1) % len(w.buckets)
		w.buckets[w.head] = bucket{startedAt: w.buckets[(w.head-1+len(w.buckets))%len(w.buckets)].startedAt.Add(w.bucketSpan)}
	}
	// Snap the newest bucket's startedAt forward to the actual now-
	// aligned span if we capped the loop above. This keeps roll()
	// idempotent after a long idle gap.
	if steps == len(w.buckets) {
		w.buckets[w.head].startedAt = now
	}
}

// record increments counters in the current bucket after rolling.
func (w *window) record(now time.Time, success bool) {
	w.roll(now)
	w.buckets[w.head].requests++
	if !success {
		w.buckets[w.head].failures++
	}
}

// totals sums requests and failures across every live bucket.
func (w *window) totals() (requests, failures int) {
	for _, b := range w.buckets {
		requests += b.requests
		failures += b.failures
	}
	return
}
