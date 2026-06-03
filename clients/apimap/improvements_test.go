package apimap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/clients/httpc"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// ── AB. httpc options passthrough ──────────────────────────────────────

func TestWithHTTPCOptions_AppliesToBuiltClient(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	yaml := strings.ReplaceAll(`clients:
  - name: x
    base_url: <BASE>
    endpoints:
      - name: ping
        method: GET
        path: /ping
`, "<BASE>", srv.URL)
	e := New()
	if err := e.LoadBytes([]byte(yaml)); err != nil {
		t.Fatal(err)
	}

	// Smuggled marker via a custom before-request hook on the httpc level.
	var seen atomic.Bool
	c, err := e.Build(WithHTTPCOptions(
		httpc.WithBeforeRequest(func(r *http.Request) {
			r.Header.Set("X-Smuggle", "yes")
			seen.Store(true)
		}),
	))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Do(context.Background(), "x.ping", Call{})
	if !seen.Load() {
		t.Error("httpc.WithBeforeRequest passed through WithHTTPCOptions did not fire")
	}
}

func TestRegisterClientOptions_PerClient(t *testing.T) {
	hitA, hitB := atomic.Int32{}, atomic.Int32{}
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-A") == "1" {
			hitA.Add(1)
		}
		w.WriteHeader(200)
	}))
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-B") == "1" {
			hitB.Add(1)
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(func() { srvA.Close(); srvB.Close() })

	yaml := `clients:
  - name: a
    base_url: ` + srvA.URL + `
    endpoints:
      - name: ping
        method: GET
        path: /a
  - name: b
    base_url: ` + srvB.URL + `
    endpoints:
      - name: ping
        method: GET
        path: /b
`
	e := New()
	if err := e.LoadBytes([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	e.RegisterClientOptions("a", httpc.WithBeforeRequest(func(r *http.Request) { r.Header.Set("X-A", "1") }))
	e.RegisterClientOptions("b", httpc.WithBeforeRequest(func(r *http.Request) { r.Header.Set("X-B", "1") }))

	c, err := e.Build()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Do(context.Background(), "a.ping", Call{})
	_, _ = c.Do(context.Background(), "b.ping", Call{})
	if hitA.Load() != 1 || hitB.Load() != 1 {
		t.Errorf("hitA=%d hitB=%d, want 1/1 (per-client opts must be isolated)", hitA.Load(), hitB.Load())
	}
}

func TestRegisterClientOptions_UnknownClientFailsBuild(t *testing.T) {
	yaml := `clients:
  - name: real
    base_url: http://localhost
    endpoints:
      - name: ping
        method: GET
        path: /
`
	e := New()
	if err := e.LoadBytes([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	e.RegisterClientOptions("ghost", httpc.WithBeforeRequest(func(*http.Request) {}))

	_, err := e.Build()
	if err == nil {
		t.Fatal("expected Build to fail for ghost client")
	}
	if !strings.Contains(err.Error(), CodeUnknownClient) {
		t.Errorf("err = %v, want %q", err, CodeUnknownClient)
	}
}

// ── C. apimap-level hooks ──────────────────────────────────────────────

func TestApimap_BeforeAfterHooks_SeeEndpointName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	t.Cleanup(srv.Close)

	yaml := `clients:
  - name: svc
    base_url: ` + srv.URL + `
    endpoints:
      - name: tick
        method: GET
        path: /tick
`
	e := New()
	_ = e.LoadBytes([]byte(yaml))

	type hookRecord struct {
		client, endpoint string
	}
	var beforeRec, afterRec hookRecord
	var afterStatus int
	var afterElapsed time.Duration

	c, err := e.Build(
		WithBeforeRequest(func(client, endpoint string, _ *http.Request) {
			beforeRec = hookRecord{client, endpoint}
		}),
		WithAfterResponse(func(client, endpoint string, _ *http.Request, resp *http.Response, _ error, d time.Duration) {
			afterRec = hookRecord{client, endpoint}
			if resp != nil {
				afterStatus = resp.StatusCode
			}
			afterElapsed = d
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Do(context.Background(), "svc.tick", Call{})

	if beforeRec.client != "svc" || beforeRec.endpoint != "tick" {
		t.Errorf("before = %+v, want {svc tick}", beforeRec)
	}
	if afterRec.client != "svc" || afterRec.endpoint != "tick" {
		t.Errorf("after = %+v, want {svc tick}", afterRec)
	}
	if afterStatus != 204 {
		t.Errorf("after status = %d, want 204", afterStatus)
	}
	if afterElapsed <= 0 {
		t.Errorf("after elapsed = %v, want > 0", afterElapsed)
	}
}

// ── E. WithDefaultCall ─────────────────────────────────────────────────

func TestWithDefaultCall_EngineWideHeaderMergesWithCaller(t *testing.T) {
	var sawAPIVer, sawTenant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAPIVer = r.URL.Query().Get("api_version")
		sawTenant = r.Header.Get("X-Tenant-ID")
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	yaml := `clients:
  - name: svc
    base_url: ` + srv.URL + `
    endpoints:
      - name: q
        method: GET
        path: /q
`
	e := New()
	_ = e.LoadBytes([]byte(yaml))
	c, err := e.Build(WithDefaultCall(Call{
		Query:   url.Values{"api_version": {"2024-11"}},
		Headers: http.Header{"X-Tenant-ID": {"42"}},
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Caller adds a different query AND overrides X-Tenant-ID. Engine
	// default still contributes api_version.
	_, _ = c.Do(context.Background(), "svc.q", Call{
		Query:   url.Values{"id": {"abc"}},
		Headers: http.Header{"X-Tenant-ID": {"99"}},
	})

	if sawAPIVer != "2024-11" {
		t.Errorf("api_version = %q, want 2024-11 (engine default)", sawAPIVer)
	}
	if sawTenant != "99" {
		t.Errorf("X-Tenant-ID = %q, want 99 (caller wins)", sawTenant)
	}
}

func TestSetClientDefaultCall_LayersBetweenEngineAndCaller(t *testing.T) {
	var sawTenant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawTenant = r.Header.Get("X-Tenant-ID")
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	yaml := `clients:
  - name: svc
    base_url: ` + srv.URL + `
    endpoints:
      - name: ping
        method: GET
        path: /p
`
	e := New()
	_ = e.LoadBytes([]byte(yaml))
	e.SetClientDefaultCall("svc", Call{Headers: http.Header{"X-Tenant-ID": {"client-default"}}})

	c, err := e.Build(WithDefaultCall(Call{
		Headers: http.Header{"X-Tenant-ID": {"engine-default"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	// No caller override → client default wins over engine default.
	_, _ = c.Do(context.Background(), "svc.ping", Call{})
	if sawTenant != "client-default" {
		t.Errorf("X-Tenant-ID = %q, want client-default", sawTenant)
	}
}

// ── D. RegisterTransport (mock mode) ──────────────────────────────────

func TestRegisterTransport_MockReplacesNetwork(t *testing.T) {
	yaml := `clients:
  - name: stripe
    base_url: https://api.stripe.com
    endpoints:
      - name: charge
        method: POST
        path: /v1/charges
        encode: json
        decode: json
`
	e := New()
	if err := e.LoadBytes([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	e.RegisterTransport("stripe", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body := io.NopCloser(strings.NewReader(`{"id":"ch_mocked"}`))
		return &http.Response{
			StatusCode: 200,
			Body:       body,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}))
	c, err := e.Build()
	if err != nil {
		t.Fatal(err)
	}

	type chargeResp struct {
		ID string `json:"id"`
	}
	got, err := Decode[chargeResp](context.Background(), c, "stripe.charge", Call{Body: map[string]string{"amount": "1000"}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.ID != "ch_mocked" {
		t.Errorf("got = %+v, want id=ch_mocked", got)
	}
}

func TestRegisterTransport_UnknownClientFailsBuild(t *testing.T) {
	yaml := `clients:
  - name: real
    base_url: http://localhost
    endpoints:
      - name: ping
        method: GET
        path: /
`
	e := New()
	_ = e.LoadBytes([]byte(yaml))
	e.RegisterTransport("phantom", roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, nil }))

	_, err := e.Build()
	if err == nil {
		t.Fatal("expected Build to fail for phantom client")
	}
	var e2 *xerrs.Error
	if !strings.Contains(err.Error(), CodeUnknownClient) {
		_ = e2
		t.Errorf("err = %v, want %q", err, CodeUnknownClient)
	}
}
