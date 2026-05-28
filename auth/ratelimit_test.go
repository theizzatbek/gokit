package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/errs"
)

// rlApp builds a tiny app: GET /h gated by mw, returns "ok".
func rlApp(mw fiber.Handler) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Get("/h", mw, func(c *fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func TestRateLimit_AllowsUpToBurst(t *testing.T) {
	// 1 rps, burst 5 — the first 5 calls in the same instant succeed,
	// the 6th is denied.
	app := rlApp(auth.RateLimit(1, 5))

	for i := 1; i <= 5; i++ {
		resp, err := app.Test(httptest.NewRequest("GET", "/h", nil))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, resp.StatusCode)
		}
	}
	resp, err := app.Test(httptest.NewRequest("GET", "/h", nil))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("request 6: status = %d, want 429", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" {
		t.Errorf("expected Retry-After header on 429")
	}
}

func TestRateLimit_ErrorCodeIsStable(t *testing.T) {
	app := rlApp(auth.RateLimit(1, 1))
	// burn the one token
	r0, _ := app.Test(httptest.NewRequest("GET", "/h", nil))
	r0.Body.Close()
	resp, _ := app.Test(httptest.NewRequest("GET", "/h", nil))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// errorHandler emits JSON {code, message}; check the code stays stable.
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	if !strings.Contains(body, "rate_limited") {
		t.Errorf("body = %q, want it to contain %q", body, auth.CodeRateLimited)
	}
}

func TestRateLimit_RefillsOverTime(t *testing.T) {
	// 10 rps, burst 1 → one allow, then ~100ms wait for refill.
	app := rlApp(auth.RateLimit(10, 1))
	r1, _ := app.Test(httptest.NewRequest("GET", "/h", nil))
	r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first request not allowed: %d", r1.StatusCode)
	}
	r2, _ := app.Test(httptest.NewRequest("GET", "/h", nil))
	r2.Body.Close()
	if r2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second request not denied: %d", r2.StatusCode)
	}
	time.Sleep(150 * time.Millisecond) // refill window
	r3, _ := app.Test(httptest.NewRequest("GET", "/h", nil))
	r3.Body.Close()
	if r3.StatusCode != http.StatusOK {
		t.Errorf("third request after refill not allowed: %d", r3.StatusCode)
	}
}

func TestRateLimitBy_IsolatesKeys(t *testing.T) {
	// Two distinct keys must NOT share the same bucket.
	keys := []string{"a", "b"}
	idx := 0
	keyFn := func(c *fiber.Ctx) string {
		k := keys[idx%2]
		idx++
		return k
	}
	app := rlApp(auth.RateLimitBy(1, 1, keyFn))

	// "a" allowed, "b" allowed, "a" denied (second hit), "b" denied.
	want := []int{200, 200, 429, 429}
	for i, w := range want {
		resp, _ := app.Test(httptest.NewRequest("GET", "/h", nil))
		resp.Body.Close()
		if resp.StatusCode != w {
			t.Errorf("hit %d: status = %d, want %d", i, resp.StatusCode, w)
		}
	}
}

func TestRateLimit_DefaultBurst(t *testing.T) {
	// burst <= 0 normalises to 1; verify a single hit goes through.
	app := rlApp(auth.RateLimit(1, 0))
	resp, _ := app.Test(httptest.NewRequest("GET", "/h", nil))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("first request blocked at status %d (burst should normalise to 1)", resp.StatusCode)
	}
}

func TestRateLimitFactory_ParsesArgs(t *testing.T) {
	cases := []struct {
		name string
		args []any
		ok   bool
	}{
		{"string args", []any{"5", "10"}, true},
		{"float string", []any{"2.5", "3"}, true},
		{"wrong arity 1", []any{"5"}, false},
		{"wrong arity 3", []any{"5", "10", "extra"}, false},
		{"bad rps", []any{"oops", "10"}, false},
		{"bad burst", []any{"5", "notanint"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := auth.RateLimitFactory(tc.args)
			if tc.ok {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if h == nil {
					t.Fatal("nil handler on ok path")
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var xe *errs.Error
				if !errors.As(err, &xe) || xe.Code != auth.CodeInvalidFactoryArgs {
					t.Errorf("err = %v, want CodeInvalidFactoryArgs", err)
				}
			}
		})
	}
}
