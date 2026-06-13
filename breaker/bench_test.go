package breaker_test

import (
	"testing"

	"github.com/theizzatbek/gokit/breaker"
)

// BenchmarkBreaker_Allow measures the closed-state hot path — the
// Allow()/done() pair wraps every protected call, so its per-op cost and
// allocations sit on the critical path of every outbound request.
func BenchmarkBreaker_Allow(b *testing.B) {
	br, err := breaker.New(breaker.Config{Name: "bench"})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		allowed, done := br.Allow()
		if !allowed {
			b.Fatal("breaker unexpectedly open")
		}
		done(true)
	}
}

// BenchmarkBreaker_Allow_Parallel is the contended variant: many
// goroutines hammering the same breaker, which is the realistic shape
// under load and exposes lock/atomic contention.
func BenchmarkBreaker_Allow_Parallel(b *testing.B) {
	br, err := breaker.New(breaker.Config{Name: "bench"})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			allowed, done := br.Allow()
			if !allowed {
				b.Fatal("breaker unexpectedly open")
			}
			done(true)
		}
	})
}
