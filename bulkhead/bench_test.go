package bulkhead_test

import (
	"context"
	"testing"

	"github.com/theizzatbek/gokit/bulkhead"
)

// BenchmarkBulkhead_Acquire measures the uncontended fast path: a single
// caller acquiring and releasing a free slot. This is the per-call
// overhead the bulkhead adds when capacity is available.
func BenchmarkBulkhead_Acquire(b *testing.B) {
	bh, err := bulkhead.New(bulkhead.Config{Name: "bench", MaxConcurrent: 1})
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		release, err := bh.Acquire(ctx)
		if err != nil {
			b.Fatal(err)
		}
		release()
	}
}

// BenchmarkBulkhead_Execute_Parallel runs many goroutines through a
// limited bulkhead, exercising the slot accounting under contention.
func BenchmarkBulkhead_Execute_Parallel(b *testing.B) {
	bh, err := bulkhead.New(bulkhead.Config{Name: "bench", MaxConcurrent: 8, MaxQueue: 1024})
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	noop := func() error { return nil }
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := bh.Execute(ctx, noop); err != nil {
				b.Fatal(err)
			}
		}
	})
}
