package httpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/theizzatbek/gokit/reqctx"
)

func TestRequestIDRoundTripper_PropagatesHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Request-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(Config{})
	if err != nil {
		t.Fatalf("httpc.New: %v", err)
	}
	req, _ := http.NewRequestWithContext(
		reqctx.WithRequestID(context.Background(), "from-test"),
		"GET", srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if got != "from-test" {
		t.Fatalf("downstream X-Request-ID = %q, want %q", got, "from-test")
	}
}

func TestRequestIDRoundTripper_DoesNotOverwriteExplicit(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Request-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(Config{})
	if err != nil {
		t.Fatalf("httpc.New: %v", err)
	}
	req, _ := http.NewRequestWithContext(
		reqctx.WithRequestID(context.Background(), "from-ctx"),
		"GET", srv.URL, nil)
	req.Header.Set("X-Request-ID", "from-caller")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if got != "from-caller" {
		t.Fatalf("downstream X-Request-ID = %q, want %q (caller wins)", got, "from-caller")
	}
}

func TestRequestIDRoundTripper_NoCtxNoHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Request-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(Config{})
	if err != nil {
		t.Fatalf("httpc.New: %v", err)
	}
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if got != "" {
		t.Fatalf("downstream X-Request-ID = %q, want empty", got)
	}
}

func TestWithoutRequestIDHeader_Suppresses(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Request-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(Config{}, WithoutRequestIDHeader())
	if err != nil {
		t.Fatalf("httpc.New: %v", err)
	}
	req, _ := http.NewRequestWithContext(
		reqctx.WithRequestID(context.Background(), "should-not-leak"),
		"GET", srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if got != "" {
		t.Fatalf("WithoutRequestIDHeader should suppress; got %q", got)
	}
}
