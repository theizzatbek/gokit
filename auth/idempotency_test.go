package auth_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/errs"
)

// idemApp wraps a handler counter that lets tests assert how many
// times the underlying handler actually ran.
type idemApp struct {
	app  *fiber.App
	hits atomic.Int32
}

func newIdemApp(mw fiber.Handler, status int, body string) *idemApp {
	a := &idemApp{app: fiber.New(fiber.Config{ErrorHandler: testErrorHandler})}
	a.app.Post("/h", mw, func(c *fiber.Ctx) error {
		a.hits.Add(1)
		c.Set("X-Request-ID", "rid-from-handler")
		return c.Status(status).SendString(body)
	})
	return a
}

func TestIdempotency_NoKey_PassesThrough(t *testing.T) {
	a := newIdemApp(auth.Idempotency(time.Hour), http.StatusOK, "ok")
	for i := 0; i < 3; i++ {
		resp, _ := a.app.Test(httptest.NewRequest("POST", "/h", nil))
		resp.Body.Close()
	}
	if a.hits.Load() != 3 {
		t.Errorf("handler hits = %d, want 3 (no key = no dedup)", a.hits.Load())
	}
}

func TestIdempotency_KeyDedupesIdenticalCalls(t *testing.T) {
	a := newIdemApp(auth.Idempotency(time.Hour), http.StatusCreated, `{"id":1}`)

	for i := 0; i < 4; i++ {
		req := httptest.NewRequest("POST", "/h", strings.NewReader(`{}`))
		req.Header.Set(auth.IdempotencyHeader, "key-abc")
		resp, _ := a.app.Test(req)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Errorf("call %d: status = %d", i, resp.StatusCode)
		}
		if string(body) != `{"id":1}` {
			t.Errorf("call %d: body = %q", i, string(body))
		}
		if i > 0 {
			if got := resp.Header.Get(auth.IdempotencyReplayHeader); got != "true" {
				t.Errorf("call %d: replay header = %q, want true", i, got)
			}
		} else if got := resp.Header.Get(auth.IdempotencyReplayHeader); got != "" {
			t.Errorf("first call should not be a replay: header = %q", got)
		}
	}
	if a.hits.Load() != 1 {
		t.Errorf("handler hits = %d, want 1 (3 retries should replay)", a.hits.Load())
	}
}

func TestIdempotency_DifferentKeys_RunTwice(t *testing.T) {
	a := newIdemApp(auth.Idempotency(time.Hour), http.StatusOK, "ok")
	for _, key := range []string{"k1", "k2"} {
		req := httptest.NewRequest("POST", "/h", nil)
		req.Header.Set(auth.IdempotencyHeader, key)
		resp, _ := a.app.Test(req)
		resp.Body.Close()
	}
	if a.hits.Load() != 2 {
		t.Errorf("handler hits = %d, want 2 (distinct keys → distinct buckets)", a.hits.Load())
	}
}

func TestIdempotency_DifferentPaths_AreNotConfused(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	mw := auth.Idempotency(time.Hour)
	var hitsA, hitsB atomic.Int32
	app.Post("/a", mw, func(c *fiber.Ctx) error { hitsA.Add(1); return c.SendString("a") })
	app.Post("/b", mw, func(c *fiber.Ctx) error { hitsB.Add(1); return c.SendString("b") })

	for _, path := range []string{"/a", "/b"} {
		req := httptest.NewRequest("POST", path, nil)
		req.Header.Set(auth.IdempotencyHeader, "same-key")
		resp, _ := app.Test(req)
		resp.Body.Close()
	}
	if hitsA.Load() != 1 || hitsB.Load() != 1 {
		t.Errorf("hits a=%d b=%d, want 1 each (same key across paths must not collide)", hitsA.Load(), hitsB.Load())
	}
}

func TestIdempotency_SafeMethods_PassThrough(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	mw := auth.Idempotency(time.Hour)
	var hits atomic.Int32
	app.Get("/h", mw, func(c *fiber.Ctx) error { hits.Add(1); return c.SendString("ok") })

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/h", nil)
		req.Header.Set(auth.IdempotencyHeader, "k1")
		resp, _ := app.Test(req)
		resp.Body.Close()
	}
	if hits.Load() != 3 {
		t.Errorf("GET hits = %d, want 3 (safe methods bypass dedup)", hits.Load())
	}
}

func TestIdempotency_5xxNotCached(t *testing.T) {
	a := newIdemApp(auth.Idempotency(time.Hour), http.StatusInternalServerError, "boom")

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/h", nil)
		req.Header.Set(auth.IdempotencyHeader, "key-5xx")
		resp, _ := a.app.Test(req)
		resp.Body.Close()
	}
	if a.hits.Load() != 3 {
		t.Errorf("handler hits = %d, want 3 (5xx must not poison the key)", a.hits.Load())
	}
}

func TestIdempotency_4xxIsCached(t *testing.T) {
	a := newIdemApp(auth.Idempotency(time.Hour), http.StatusBadRequest, "no")

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/h", nil)
		req.Header.Set(auth.IdempotencyHeader, "key-4xx")
		resp, _ := a.app.Test(req)
		resp.Body.Close()
	}
	if a.hits.Load() != 1 {
		t.Errorf("4xx with same key should replay: handler hits = %d, want 1", a.hits.Load())
	}
}

func TestIdempotency_ExpiredKeyReRunsHandler(t *testing.T) {
	a := newIdemApp(auth.Idempotency(20*time.Millisecond), http.StatusOK, "ok")

	req := httptest.NewRequest("POST", "/h", nil)
	req.Header.Set(auth.IdempotencyHeader, "ttl-test")
	resp1, _ := a.app.Test(req)
	resp1.Body.Close()

	time.Sleep(40 * time.Millisecond)

	req2 := httptest.NewRequest("POST", "/h", nil)
	req2.Header.Set(auth.IdempotencyHeader, "ttl-test")
	resp2, _ := a.app.Test(req2)
	resp2.Body.Close()

	if a.hits.Load() != 2 {
		t.Errorf("handler hits = %d, want 2 (entry should have expired)", a.hits.Load())
	}
}

func TestIdempotency_ReplaysContentTypeAndHeaders(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	mw := auth.Idempotency(time.Hour)
	app.Post("/h", mw, func(c *fiber.Ctx) error {
		c.Set("Location", "/created/42")
		return c.Status(http.StatusCreated).JSON(map[string]int{"id": 42})
	})

	req := httptest.NewRequest("POST", "/h", nil)
	req.Header.Set(auth.IdempotencyHeader, "rep-headers")
	r1, _ := app.Test(req)
	r1.Body.Close()

	req2 := httptest.NewRequest("POST", "/h", nil)
	req2.Header.Set(auth.IdempotencyHeader, "rep-headers")
	r2, _ := app.Test(req2)
	body, _ := io.ReadAll(r2.Body)
	r2.Body.Close()

	if r2.Header.Get("Content-Type") != "application/json" {
		t.Errorf("replay Content-Type = %q", r2.Header.Get("Content-Type"))
	}
	if r2.Header.Get("Location") != "/created/42" {
		t.Errorf("replay Location = %q", r2.Header.Get("Location"))
	}
	if !strings.Contains(string(body), `"id":42`) {
		t.Errorf("replay body = %q", string(body))
	}
}

func TestIdempotencyFactory_ParsesTTL(t *testing.T) {
	cases := []struct {
		name string
		args []any
		ok   bool
	}{
		{"valid string", []any{"10m"}, true},
		{"valid hours", []any{"24h"}, true},
		{"wrong arity", []any{"10m", "extra"}, false},
		{"bad string", []any{"not a duration"}, false},
		{"non-string", []any{123}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := auth.IdempotencyFactory(tc.args)
			if tc.ok {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if h == nil {
					t.Fatal("nil handler on ok path")
				}
			} else {
				var xe *errs.Error
				if err == nil || !errors.As(err, &xe) || xe.Code != auth.CodeInvalidFactoryArgs {
					t.Errorf("err = %v, want CodeInvalidFactoryArgs", err)
				}
			}
		})
	}
}

func TestIdempotency_HandlerErrorNotCached(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	var hits atomic.Int32
	app.Post("/h", auth.Idempotency(time.Hour), func(c *fiber.Ctx) error {
		hits.Add(1)
		return errs.Unavailable("svc_down", "downstream offline")
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/h", nil)
		req.Header.Set(auth.IdempotencyHeader, "err-key")
		resp, _ := app.Test(req)
		resp.Body.Close()
	}
	if hits.Load() != 2 {
		t.Errorf("handler hits = %d, want 2 (returned errors must not be cached)", hits.Load())
	}
}
