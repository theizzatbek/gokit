package httpc_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/theizzatbek/gokit/clients/httpc"
)

// Example builds a hardened *http.Client and uses it exactly like the
// stdlib one — retries, per-attempt timeout, and request-ID propagation
// are wired in transparently.
func Example() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "pong")
	}))
	defer srv.Close()

	client, err := httpc.New(httpc.Config{Timeout: 2 * time.Second})
	if err != nil {
		panic(err)
	}

	resp, err := client.Get(srv.URL)
	if err != nil {
		panic(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	fmt.Println(resp.StatusCode)
	fmt.Print(string(body))
	// Output:
	// 200
	// pong
}

// ExampleNew_retryOnServerError shows the automatic retry on a retryable
// status (408/429/5xx) for idempotent requests. The upstream fails twice
// with 503 before succeeding; the client transparently retries and the
// caller only sees the final 200.
func ExampleNew_retryOnServerError() {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// BackoffBase is tiny so the example runs fast; MaxRetries defaults to 3.
	client, err := httpc.New(httpc.Config{
		Timeout:     time.Second,
		BackoffBase: time.Millisecond,
	})
	if err != nil {
		panic(err)
	}

	resp, err := client.Get(srv.URL)
	if err != nil {
		panic(err)
	}
	defer func() { _ = resp.Body.Close() }()

	fmt.Println("final status:", resp.StatusCode)
	fmt.Println("attempts:", atomic.LoadInt32(&attempts))
	// Output:
	// final status: 200
	// attempts: 3
}
