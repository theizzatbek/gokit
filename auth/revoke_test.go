package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestMemRevokedAccessStore_RoundTrip(t *testing.T) {
	s := NewMemRevokedAccessStore()
	ctx := context.Background()

	if revoked, _ := s.IsRevoked(ctx, "j1"); revoked {
		t.Error("fresh store should not report any jti as revoked")
	}
	if err := s.Revoke(ctx, "j1", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if revoked, _ := s.IsRevoked(ctx, "j1"); !revoked {
		t.Error("after Revoke, IsRevoked should return true")
	}
}

func TestMemRevokedAccessStore_ExpiryLazyGC(t *testing.T) {
	s := NewMemRevokedAccessStore()
	s.now = func() time.Time { return time.Unix(1000, 0) }
	_ = s.Revoke(context.Background(), "j1", time.Unix(2000, 0))
	if rev, _ := s.IsRevoked(context.Background(), "j1"); !rev {
		t.Fatal("must be revoked at t=1000")
	}
	s.now = func() time.Time { return time.Unix(3000, 0) }
	if rev, _ := s.IsRevoked(context.Background(), "j1"); rev {
		t.Fatal("must be auto-expired at t=3000")
	}
}

func TestBearer_RevokedAccessStore_Rejects(t *testing.T) {
	store := NewMemRevokedAccessStore()
	ks, _ := GenerateEd25519Key("k1")
	a, err := New[testClaims](Config{
		Keys: ks, AccessTTL: time.Minute, RefreshTTL: time.Hour,
	}, WithRevokedAccessStore(store))
	if err != nil {
		t.Fatal(err)
	}

	tok, err := a.Sign(Claims[testClaims]{
		Subject:   "u-1",
		JTI:       "j-revoked",
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
		IssuedAt:  time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Revoke(context.Background(), "j-revoked", time.Now().Add(time.Minute))

	app := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, e error) error {
		if x, ok := e.(*xerrs.Error); ok {
			return c.Status(http.StatusUnauthorized).SendString(x.Code)
		}
		return c.Status(http.StatusInternalServerError).SendString(e.Error())
	}})
	app.Get("/me", a.Bearer(BearerRequired), func(c *fiber.Ctx) error { return c.SendString("ok") })

	req := httptest.NewRequest("GET", "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ := app.Test(req, -1)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Www-Authenticate"), CodeTokenRevoked) {
		t.Errorf("WWW-Authenticate missing %q: %q", CodeTokenRevoked, resp.Header.Get("Www-Authenticate"))
	}
}

func TestBearer_RevokedAccessStore_FailOpenOnBackendError(t *testing.T) {
	a, err := New[testClaims](Config{
		Keys: mustNewAuth(t).eng.keySet(), AccessTTL: time.Minute, RefreshTTL: time.Hour,
	}, WithRevokedAccessStore(brokenStore{}))
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := a.Sign(Claims[testClaims]{
		Subject:   "u",
		JTI:       "j",
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
		IssuedAt:  time.Now().Unix(),
	})
	app := fiber.New()
	app.Get("/me", a.Bearer(BearerRequired), func(c *fiber.Ctx) error { return c.SendString("ok") })
	req := httptest.NewRequest("GET", "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ := app.Test(req, -1)
	// fail-OPEN: token still passes even though the store errored.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (fail-open on backend err)", resp.StatusCode)
	}
}

type brokenStore struct{}

func (brokenStore) IsRevoked(context.Context, string) (bool, error) {
	return false, xerrs.Internal("boom", "backend down")
}
func (brokenStore) Revoke(context.Context, string, time.Time) error { return nil }
