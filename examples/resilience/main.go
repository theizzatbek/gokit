// Command resilience is a single-process demo of the kit's three
// outbound-HTTP resilience features composed together: circuit breaker,
// concurrency bulkhead, and YAML-declarative apimap.
//
// What happens:
//
//  1. An httptest server with a tunable failure mode. By default it
//     fails every other request with 503; flip "halfOpenServer = false"
//     to make it fail the first 30 then recover, etc.
//  2. apimap.Engine loads clients.yaml, which declares ONE upstream
//     ("flaky") with both `breaker:` and `bulkhead:` blocks.
//  3. We fire 30 concurrent requests through the wrapped client. Some
//     hit the server, some are short-circuited by the breaker, some
//     fail-fast from the bulkhead full queue.
//  4. At the end we Gather the prometheus registry and print the
//     breaker / bulkhead / apimap collectors so you can see the curves.
//
// Run:
//
//	go run ./examples/resilience
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/breaker"
	"github.com/theizzatbek/gokit/bulkhead"
	"github.com/theizzatbek/gokit/clients/apimap"
)

// pingResponse is the typed payload Decode reads back from /ping.
type pingResponse struct {
	OK bool `json:"ok"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fail:", err)
		os.Exit(1)
	}
}

func run() error {
	// 1. The flaky upstream. Every request either returns 503 (failure),
	//    sleeps 300ms then 200 (slow), or 200 immediately.
	server := flakyServer()
	defer server.Close()
	os.Setenv("UPSTREAM_BASE_URL", server.URL)

	// 2. apimap engine. Build wires breaker + bulkhead automatically.
	reg := prometheus.NewRegistry()
	eng := apimap.New()
	if err := eng.LoadFile("examples/resilience/clients.yaml"); err != nil {
		return fmt.Errorf("LoadFile: %w", err)
	}
	apimap.RegisterResponse[pingResponse](eng, "flaky.ping")
	client, err := eng.Build(apimap.WithMetrics(reg))
	if err != nil {
		return fmt.Errorf("Build: %w", err)
	}

	// 3. 30 concurrent callers, 2 batches separated by a small pause so
	//    the breaker has time to open after the first batch trips it.
	fmt.Println("Batch 1 — server is healthy-ish; some 5xx, some slow:")
	stats := fireBatch(client, 30)
	stats.print()

	fmt.Println("\nBatch 2 — wait 3s past the breaker's OpenInterval=2s, expect recovery:")
	time.Sleep(3 * time.Second)
	stats = fireBatch(client, 30)
	stats.print()

	// 4. Print the kit-emitted collectors.
	fmt.Println("\nFinal collector snapshot:")
	printCollectors(reg)
	return nil
}

func fireBatch(client *apimap.Client, n int) *batchStats {
	stats := &batchStats{}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := apimap.Decode[pingResponse](ctx, client, "flaky.ping", apimap.Call{})
			stats.record(err)
		}()
	}
	wg.Wait()
	return stats
}

type batchStats struct {
	ok           atomic.Int64
	circuitOpen  atomic.Int64
	bulkheadFull atomic.Int64
	other        atomic.Int64
}

func (s *batchStats) record(err error) {
	switch {
	case err == nil:
		s.ok.Add(1)
	case errors.Is(err, breaker.ErrOpen):
		s.circuitOpen.Add(1)
	case errors.Is(err, bulkhead.ErrBulkheadFull):
		s.bulkheadFull.Add(1)
	default:
		s.other.Add(1)
	}
}

func (s *batchStats) print() {
	fmt.Printf("  ok=%d  circuit_open=%d  bulkhead_full=%d  other=%d\n",
		s.ok.Load(), s.circuitOpen.Load(), s.bulkheadFull.Load(), s.other.Load())
}

func flakyServer() *httptest.Server {
	var calls atomic.Int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := calls.Add(1)
		switch {
		case i%3 == 0:
			// 503 — drives the breaker towards Open.
			w.WriteHeader(http.StatusServiceUnavailable)
		case i%5 == 0:
			// Slow — the bulkhead's MaxConcurrent=3 caps how many of
			// these can be in-flight together.
			time.Sleep(300 * time.Millisecond)
			fallthrough
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pingResponse{OK: true})
		}
	}))
}

func printCollectors(reg *prometheus.Registry) {
	families, err := reg.Gather()
	if err != nil {
		fmt.Println("Gather:", err)
		return
	}
	for _, mf := range families {
		name := mf.GetName()
		// Filter to the interesting families — there are 20+ otherwise.
		switch name {
		case
			"breaker_state",
			"breaker_transitions_total",
			"breaker_short_circuits_total",
			"breaker_requests_total",
			"bulkhead_in_flight",
			"bulkhead_capacity",
			"bulkhead_acquires_total",
			"apimap_requests_total":
		default:
			continue
		}
		for _, m := range mf.Metric {
			labels := []string{}
			for _, l := range m.Label {
				labels = append(labels, fmt.Sprintf("%s=%s", l.GetName(), l.GetValue()))
			}
			val := ""
			switch {
			case m.Counter != nil:
				val = fmt.Sprintf("%.0f", m.Counter.GetValue())
			case m.Gauge != nil:
				val = fmt.Sprintf("%.0f", m.Gauge.GetValue())
			case m.Histogram != nil:
				val = fmt.Sprintf("count=%d sum=%.3f", m.Histogram.GetSampleCount(), m.Histogram.GetSampleSum())
			}
			fmt.Printf("  %s{%s} %s\n", name, joinComma(labels), val)
		}
	}
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
