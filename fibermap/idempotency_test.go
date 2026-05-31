package fibermap

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// memStore is a goroutine-safe in-memory IdempotencyStore for tests.
type memStore struct {
	mu    sync.Mutex
	data  map[string]*StoredResponse
	calls int32
}

func newMemStore() *memStore { return &memStore{data: map[string]*StoredResponse{}} }

func (s *memStore) Get(_ context.Context, key string) (*StoredResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	atomic.AddInt32(&s.calls, 1)
	return s.data[key], nil
}

func (s *memStore) Set(_ context.Context, key string, resp *StoredResponse, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = resp
	return nil
}

func TestIdempotency_CapturesAndReplaysBody(t *testing.T) {
	store := newMemStore()
	app := fiber.New()
	var hits int32
	app.Post("/x",
		IdempotencyKey(store),
		func(c *fiber.Ctx) error {
			atomic.AddInt32(&hits, 1)
			return c.Status(fiber.StatusCreated).
				JSON(fiber.Map{"id": "abc", "hit": atomic.LoadInt32(&hits)})
		})

	req := httptest.NewRequest("POST", "/x", nil)
	req.Header.Set("X-Idempotency-Key", "k1")
	resp1, _ := app.Test(req)
	body1, _ := io.ReadAll(resp1.Body)

	req2 := httptest.NewRequest("POST", "/x", nil)
	req2.Header.Set("X-Idempotency-Key", "k1")
	resp2, _ := app.Test(req2)
	body2, _ := io.ReadAll(resp2.Body)

	if resp1.StatusCode != fiber.StatusCreated || resp2.StatusCode != fiber.StatusCreated {
		t.Errorf("status1=%d status2=%d, both should be 201", resp1.StatusCode, resp2.StatusCode)
	}
	if string(body1) != string(body2) {
		t.Errorf("body1=%s body2=%s, replay must match", body1, body2)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("handler hits = %d, want 1 (second call should replay)", hits)
	}
	if got := resp2.Header.Get("X-Idempotent-Replay"); got != "true" {
		t.Errorf("X-Idempotent-Replay = %q, want true on replay", got)
	}
	if resp1.Header.Get("X-Idempotent-Replay") != "" {
		t.Errorf("X-Idempotent-Replay must NOT appear on the first request")
	}
}

func TestIdempotency_DifferentKey_HandlerRunsAgain(t *testing.T) {
	store := newMemStore()
	app := fiber.New()
	var hits int32
	app.Post("/x",
		IdempotencyKey(store),
		func(c *fiber.Ctx) error {
			atomic.AddInt32(&hits, 1)
			return c.Status(fiber.StatusCreated).SendString("ok")
		})

	for _, k := range []string{"k1", "k2"} {
		req := httptest.NewRequest("POST", "/x", nil)
		req.Header.Set("X-Idempotency-Key", k)
		_, _ = app.Test(req)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Errorf("hits = %d, want 2 (distinct keys → independent caches)", hits)
	}
}

func TestIdempotency_MissingHeader_PassesThrough(t *testing.T) {
	store := newMemStore()
	app := fiber.New()
	var hits int32
	app.Post("/x",
		IdempotencyKey(store),
		func(c *fiber.Ctx) error {
			atomic.AddInt32(&hits, 1)
			return c.Status(fiber.StatusCreated).SendString("ok")
		})

	_, _ = app.Test(httptest.NewRequest("POST", "/x", nil))
	_, _ = app.Test(httptest.NewRequest("POST", "/x", nil))
	if atomic.LoadInt32(&hits) != 2 {
		t.Errorf("hits = %d, want 2 (no header → no caching)", hits)
	}
}

func TestIdempotency_MissingHeader_Required_Returns400(t *testing.T) {
	store := newMemStore()
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(nil)})
	app.Post("/x",
		IdempotencyKey(store, WithIdempotencyRequired()),
		func(c *fiber.Ctx) error { return c.SendString("ok") })

	resp, _ := app.Test(httptest.NewRequest("POST", "/x", nil))
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &body)
	if got, _ := body["code"].(string); got != CodeIdempotencyKeyMissing {
		t.Errorf("code = %v, want %s", body["code"], CodeIdempotencyKeyMissing)
	}
}

func TestIdempotency_GET_NotCachedByDefault(t *testing.T) {
	store := newMemStore()
	app := fiber.New()
	var hits int32
	app.Get("/x",
		IdempotencyKey(store),
		func(c *fiber.Ctx) error {
			atomic.AddInt32(&hits, 1)
			return c.SendString("ok")
		})

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Idempotency-Key", "k1")
	_, _ = app.Test(req)
	req2 := httptest.NewRequest("GET", "/x", nil)
	req2.Header.Set("X-Idempotency-Key", "k1")
	_, _ = app.Test(req2)

	if atomic.LoadInt32(&hits) != 2 {
		t.Errorf("hits = %d, want 2 (GET should pass through by default)", hits)
	}
}

func TestIdempotency_5xxNotCached(t *testing.T) {
	store := newMemStore()
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(nil)})
	var hits int32
	app.Post("/x",
		IdempotencyKey(store),
		func(c *fiber.Ctx) error {
			atomic.AddInt32(&hits, 1)
			return c.Status(fiber.StatusInternalServerError).SendString("nope")
		})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/x", nil)
		req.Header.Set("X-Idempotency-Key", "k1")
		_, _ = app.Test(req)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Errorf("hits = %d, want 2 (5xx should not be cached so retry succeeds)", hits)
	}
}

func TestIdempotency_OverMaxBodySize_PassesThrough(t *testing.T) {
	store := newMemStore()
	app := fiber.New()
	var hits int32
	app.Post("/x",
		IdempotencyKey(store, WithIdempotencyMaxBodySize(8)),
		func(c *fiber.Ctx) error {
			atomic.AddInt32(&hits, 1)
			return c.SendString("body-much-longer-than-eight")
		})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/x", nil)
		req.Header.Set("X-Idempotency-Key", "k1")
		_, _ = app.Test(req)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Errorf("hits = %d, want 2 (oversize body should not cache)", hits)
	}
}

func TestIdempotency_CustomHeaderName(t *testing.T) {
	store := newMemStore()
	app := fiber.New()
	var hits int32
	app.Post("/x",
		IdempotencyKey(store, WithIdempotencyHeader("X-Req-Token")),
		func(c *fiber.Ctx) error {
			atomic.AddInt32(&hits, 1)
			return c.SendString("ok")
		})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/x", nil)
		req.Header.Set("X-Req-Token", "abc")
		_, _ = app.Test(req)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("hits = %d, want 1 with custom header", hits)
	}
}
