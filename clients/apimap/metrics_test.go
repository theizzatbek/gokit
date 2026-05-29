package apimap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestMetrics_OneRequestPerStatusClass(t *testing.T) {
	cases := []struct {
		path        string
		status      int
		statusClass string
	}{
		{"/ok", 200, "2xx"},
		{"/created", 201, "2xx"},
		{"/notfound", 404, "4xx"},
		{"/boom", 500, "5xx"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, tc := range cases {
			if r.URL.Path == tc.path {
				w.WriteHeader(tc.status)
				return
			}
		}
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)

	reg := prometheus.NewRegistry()
	yaml := strings.ReplaceAll(`clients:
  - name: srv
    base_url: <BASE>
    endpoints:
      - name: ok
        method: GET
        path: /ok
      - name: created
        method: GET
        path: /created
      - name: notfound
        method: GET
        path: /notfound
      - name: boom
        method: GET
        path: /boom
`, "<BASE>", srv.URL)

	e := New()
	if err := e.LoadBytes([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	c, err := e.Build(WithMetrics(reg))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range cases {
		ep := "srv." + strings.TrimPrefix(tc.path, "/")
		resp, err := c.Do(context.Background(), ep, Call{})
		if err != nil {
			t.Fatalf("Do %s: %v", ep, err)
		}
		_ = resp.Body.Close()
	}

	for _, tc := range cases {
		ep := strings.TrimPrefix(tc.path, "/")
		v := apimapCounter(t, reg, "srv", ep, tc.statusClass)
		if v != 1 {
			t.Errorf("requests_total{client=srv,endpoint=%s,status=%s} = %v, want 1", ep, tc.statusClass, v)
		}
		// duration histogram must have a sample with the same labels
		if !apimapDurationObserved(t, reg, "srv", ep, tc.statusClass) {
			t.Errorf("request_duration_seconds{client=srv,endpoint=%s,status=%s} not observed", ep, tc.statusClass)
		}
	}
}

func TestMetrics_TransportError_LabelsAsError(t *testing.T) {
	reg := prometheus.NewRegistry()
	// 127.0.0.1:1 is reserved and almost certainly refuses connections.
	yaml := `clients:
  - name: dead
    base_url: http://127.0.0.1:1
    timeout: 50ms
    max_retries: 0
    endpoints:
      - name: probe
        method: GET
        path: /
`
	e := New()
	if err := e.LoadBytes([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	c, err := e.Build(WithMetrics(reg))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(context.Background(), "dead.probe", Call{}); err == nil {
		t.Fatal("expected transport error, got nil")
	}
	if v := apimapCounter(t, reg, "dead", "probe", "error"); v != 1 {
		t.Errorf("requests_total{client=dead,endpoint=probe,status=error} = %v, want 1", v)
	}
}

func TestMetrics_NoWithMetrics_NoCollectorsAndNoPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	c := buildClientWithYAML(t, `clients:
  - name: srv
    base_url: <BASE>
    endpoints:
      - name: ping
        method: GET
        path: /
`, srv.URL)
	if c.metrics != nil {
		t.Fatalf("expected nil metrics when WithMetrics omitted, got %#v", c.metrics)
	}
	resp, err := c.Do(context.Background(), "srv.ping", Call{})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
}

func apimapCounter(t *testing.T, reg *prometheus.Registry, client, endpoint, status string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "apimap_requests_total" {
			continue
		}
		for _, m := range mf.Metric {
			if labelEq(m, "client", client) && labelEq(m, "endpoint", endpoint) && labelEq(m, "status", status) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func apimapDurationObserved(t *testing.T, reg *prometheus.Registry, client, endpoint, status string) bool {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "apimap_request_duration_seconds" {
			continue
		}
		for _, m := range mf.Metric {
			if labelEq(m, "client", client) && labelEq(m, "endpoint", endpoint) && labelEq(m, "status", status) {
				return m.GetHistogram().GetSampleCount() > 0
			}
		}
	}
	return false
}

func labelEq(m *dto.Metric, name, want string) bool {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue() == want
		}
	}
	return false
}