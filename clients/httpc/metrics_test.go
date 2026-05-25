package httpc

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewCollectors_NilRegisterer(t *testing.T) {
	c := newCollectors(nil)
	if c != nil {
		t.Fatalf("newCollectors(nil) = %v, want nil (no collectors when no registerer)", c)
	}
}

func TestNewCollectors_RegistersAllMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := newCollectors(reg)
	if c == nil {
		t.Fatal("newCollectors(reg) = nil, want non-nil")
	}
	want := []string{
		"httpc_requests_total",
		"httpc_request_duration_seconds",
		"httpc_retries_total",
		"httpc_retries_exhausted_total",
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	present := map[string]bool{}
	for _, mf := range mfs {
		present[mf.GetName()] = true
	}
	for _, name := range want {
		if !present[name] {
			t.Errorf("metric %q not registered", name)
		}
	}
}

func TestMetricsTransport_CountsRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	reg := prometheus.NewRegistry()
	c := newCollectors(reg)
	mt := &metricsTransport{base: http.DefaultTransport, collectors: c}

	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest("GET", srv.URL, nil)
		resp, err := mt.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	got := testutil.ToFloat64(c.requestsTotal.WithLabelValues("GET", "200"))
	if got != 3 {
		t.Errorf("httpc_requests_total{GET,200} = %v, want 3", got)
	}
}

func TestMetricsTransport_NilCollectorsIsNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	mt := &metricsTransport{base: http.DefaultTransport, collectors: nil}
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := mt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}
