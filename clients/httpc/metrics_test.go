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
	if c.requestsTotal == nil {
		t.Error("requestsTotal collector is nil")
	}
	if c.requestDuration == nil {
		t.Error("requestDuration collector is nil")
	}
	if c.retriesTotal == nil {
		t.Error("retriesTotal collector is nil")
	}
	if c.retriesExhausted == nil {
		t.Error("retriesExhausted collector is nil")
	}
	// Re-registering with the same registry must fail — proves they were
	// registered the first time without us depending on Gather() returning
	// uninitialised series.
	err := reg.Register(c.requestsTotal)
	if err == nil {
		t.Error("re-Register of requestsTotal succeeded; expected AlreadyRegisteredError")
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
