package httpc

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestNew_InvalidConfig(t *testing.T) {
	_, err := New(Config{Timeout: -1})
	if err == nil {
		t.Fatal("New() = nil error, want validation error")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeInvalidTimeout {
		t.Errorf("err = %v, want *xerrs.Error{Code: %q}", err, CodeInvalidTimeout)
	}
}

func TestNew_ReturnsClientWithZeroTimeout(t *testing.T) {
	c, err := New(Config{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if c.Timeout != 0 {
		t.Errorf("client.Timeout = %v, want 0 (per-attempt lives in transport)", c.Timeout)
	}
	if c.Transport == nil {
		t.Error("client.Transport is nil")
	}
}

func TestNew_EndToEnd_RetriesAndSucceeds(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&n, 1)
		if count < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{
		Timeout:     time.Second,
		MaxRetries:  3,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestNew_WithMetrics_CountsEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	reg := prometheus.NewRegistry()
	c, err := New(Config{Timeout: time.Second}, WithMetrics(reg))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	mfs, _ := reg.Gather()
	var sum float64
	for _, mf := range mfs {
		if mf.GetName() != "httpc_requests_total" {
			continue
		}
		for _, m := range mf.Metric {
			sum += m.GetCounter().GetValue()
		}
	}
	if sum != 2 {
		t.Errorf("httpc_requests_total sum = %v, want 2", sum)
	}
}

type clientTestRoundTripFunc func(*http.Request) (*http.Response, error)

func (f clientTestRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestNewTransport_WithBaseTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	var calls int32
	custom := clientTestRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return http.DefaultTransport.RoundTrip(req)
	})

	rt, err := NewTransport(Config{Timeout: time.Second}, WithBaseTransport(custom))
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Transport: rt}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("custom base transport invoked %d times, want 1", got)
	}
}
