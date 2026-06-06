package auth

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth/internal/principalkey"
	"github.com/theizzatbek/gokit/errs"
)

func TestFrom_ReturnsNilWhenAbsent(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		p, ok := From[testClaims](c)
		if p != nil || ok {
			t.Fatalf("expected (nil,false), got (%v,%v)", p, ok)
		}
		return c.SendStatus(204)
	})
	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != 204 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestFrom_ReturnsStoredPrincipal(t *testing.T) {
	app := fiber.New()
	want := &Principal[testClaims]{Subject: "u-1", Scopes: []string{"a"}, Claims: testClaims{TenantID: "t-1"}}
	app.Get("/", func(c *fiber.Ctx) error {
		c.Locals(principalkey.Key{}, want)
		got, ok := From[testClaims](c)
		if !ok || got != want {
			t.Fatalf("From = (%v,%v), want %v,true", got, ok, want)
		}
		return c.SendStatus(204)
	})
	app.Test(httptest.NewRequest("GET", "/", nil))
}

func TestMustFrom_Returns500WhenAbsent(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		_, err := MustFrom[testClaims](c)
		var e *errs.Error
		if !errors.As(err, &e) || e.Kind != errs.KindInternal {
			t.Fatalf("err = %v, want Internal", err)
		}
		return c.SendStatus(204)
	})
	app.Test(httptest.NewRequest("GET", "/", nil))
}

func TestSubject_HelperReadsFrom(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		c.Locals(principalkey.Key{}, &Principal[testClaims]{Subject: "u-7"})
		if Subject[testClaims](c) != "u-7" {
			t.Fatalf("Subject = %q", Subject[testClaims](c))
		}
		return c.SendStatus(204)
	})
	app.Test(httptest.NewRequest("GET", "/", nil))
}

func TestHasScope_True(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		c.Locals(principalkey.Key{}, &Principal[testClaims]{Scopes: []string{"posts:read", "posts:write"}})
		if !HasScope[testClaims](c, "posts:write") {
			t.Fatalf("HasScope = false, want true")
		}
		if HasScope[testClaims](c, "missing") {
			t.Fatalf("HasScope(missing) = true")
		}
		return c.SendStatus(204)
	})
	app.Test(httptest.NewRequest("GET", "/", nil))
}

func TestPrincipal_ExpiresParsedFromUnix(t *testing.T) {
	p := &Principal[testClaims]{Expires: time.Unix(1_700_000_900, 0)}
	if p.Expires.Unix() != 1_700_000_900 {
		t.Fatalf("Expires roundtrip broken")
	}
}
