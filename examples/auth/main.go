// Example: a minimal fibermap service that exercises the full auth bundle —
// Login → protected GET /me → POST /posts (require_scope) → Refresh → Logout.
// One in-memory user; argon2id hash computed at startup.
package main

import (
	"context"
	_ "embed"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/fibermap"
	"github.com/theizzatbek/fibermap/auth"
	"github.com/theizzatbek/fibermap/auth/fibermount"
	"github.com/theizzatbek/fibermap/errs"
)

//go:embed routes.yaml
var routesYAML []byte

type appClaims struct {
	TenantID string `json:"tenant_id,omitempty"`
}

type appCtx struct {
	UserID string
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	app, err := buildApp(logger)
	if err != nil {
		logger.Error("build failed", "err", err)
		os.Exit(1)
	}
	addr := ":8080"
	logger.Info("listening", "addr", addr)
	if err := app.Listen(addr); err != nil {
		logger.Error("listen failed", "err", err)
		os.Exit(1)
	}
}

// buildApp wires the example. Extracted so the test can drive the same app
// through app.Test without binding a port.
func buildApp(logger *slog.Logger) (*fiber.App, error) {
	hashedPw, err := auth.DefaultHasher.Hash("hunter2")
	if err != nil {
		return nil, err
	}
	type user struct {
		ID, Password string
		Scopes       []string
		TenantID     string
	}
	users := map[string]user{
		"alice": {ID: "u-1", Password: hashedPw, Scopes: []string{"posts:write"}, TenantID: "t-9"},
	}

	keys, err := auth.GenerateEd25519Key("k1")
	if err != nil {
		return nil, err
	}
	a, err := auth.New[appClaims](auth.Config{
		Issuer:     "example",
		Audience:   []string{"web"},
		Keys:       keys,
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 30 * 24 * time.Hour,
	},
		auth.WithRefreshStore(newMemStore()),
		auth.WithCookieSecure(false),
		auth.WithLogger(logger),
	)
	if err != nil {
		return nil, err
	}
	a.SetCredentialsVerifier(func(ctx context.Context, req auth.LoginRequest) (auth.LoginResult[appClaims], error) {
		u, ok := users[req.Login]
		if !ok {
			return auth.LoginResult[appClaims]{}, errs.Unauthorized(auth.CodeInvalidCredentials, "invalid login or password")
		}
		if err := auth.DefaultHasher.Verify(u.Password, req.Password); err != nil {
			return auth.LoginResult[appClaims]{}, errs.Unauthorized(auth.CodeInvalidCredentials, "invalid login or password")
		}
		return auth.LoginResult[appClaims]{
			Subject: u.ID,
			Scopes:  u.Scopes,
			Custom:  appClaims{TenantID: u.TenantID},
		}, nil
	})

	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(logger)})

	eng := fibermap.New[appCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) {
		if p, ok := auth.From[appClaims](c); ok {
			return appCtx{UserID: p.Subject}, nil
		}
		return appCtx{}, nil
	})

	// Adapt the auth handlers (fiber.Handler) into fibermap's typed
	// HandlerFunc[appCtx]. The adapter just forwards c.Ctx — the auth
	// handlers operate on the bare Fiber context.
	fibermap.RegisterHandler(eng, "auth.login", wrapFiber[appCtx](a.LoginHandler))
	fibermap.RegisterHandler(eng, "auth.refresh", wrapFiber[appCtx](a.RefreshHandler))
	fibermap.RegisterHandler(eng, "auth.logout", wrapFiber[appCtx](a.LogoutHandler))

	fibermap.RegisterHandler(eng, "app.me", func(c *fibermap.Context[appCtx]) error {
		p, err := auth.MustFrom[appClaims](c.Ctx)
		if err != nil {
			return err
		}
		return c.JSON(map[string]any{
			"user_id":   p.Subject,
			"tenant_id": p.Claims.TenantID,
			"scopes":    p.Scopes,
		})
	})
	fibermap.RegisterHandler(eng, "app.create_post", func(c *fibermap.Context[appCtx]) error {
		return c.Status(http.StatusCreated).JSON(map[string]string{"status": "created"})
	})

	// Bridge auth's middleware factories into fibermap's typed signature in
	// one call. fibermount lives in its own subpackage so the core auth/
	// package never imports fibermap.
	if err := fibermount.MountMiddlewareFactories(eng, a); err != nil {
		return nil, err
	}

	if err := eng.LoadBytes(routesYAML); err != nil {
		return nil, err
	}
	if err := eng.Mount(app); err != nil {
		return nil, err
	}
	return app, nil
}

// wrapFiber adapts a plain fiber.Handler into a fibermap.HandlerFunc[T] by
// forwarding the embedded *fiber.Ctx. Used for handlers that do not need
// the typed Data payload (e.g. the auth bundle's login/refresh/logout).
func wrapFiber[T any](h fiber.Handler) fibermap.HandlerFunc[T] {
	return func(c *fibermap.Context[T]) error { return h(c.Ctx) }
}

// memStore is a tiny in-memory auth.RefreshStore for the example. Production
// services use auth/refreshpg or auth/refreshredis. This deliberately mirrors
// the smallest subset of auth/internal/memstore — the latter is internal to
// the auth package and not importable from examples/.
type memStore struct {
	mu       sync.Mutex
	records  map[[32]byte]*auth.Record
	families map[string][][32]byte
	subjects map[string][][32]byte
}

func newMemStore() *memStore {
	return &memStore{
		records:  make(map[[32]byte]*auth.Record),
		families: make(map[string][][32]byte),
		subjects: make(map[string][][32]byte),
	}
}

func (m *memStore) Issue(_ context.Context, r auth.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	dup := r
	m.records[r.TokenHash] = &dup
	m.families[r.FamilyID] = append(m.families[r.FamilyID], r.TokenHash)
	if r.Subject != "" {
		m.subjects[r.Subject] = append(m.subjects[r.Subject], r.TokenHash)
	}
	return nil
}

func (m *memStore) Consume(_ context.Context, h [32]byte, now time.Time) (auth.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[h]
	if !ok {
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshInvalid, "refresh token unknown")
	}
	if rec.RevokedAt != nil || rec.ConsumedAt != nil {
		m.revokeFamilyLocked(rec.FamilyID, now)
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshReused, "refresh token reused")
	}
	if !rec.ExpiresAt.After(now) {
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshExpired, "refresh token expired")
	}
	t := now
	rec.ConsumedAt = &t
	return *rec, nil
}

func (m *memStore) RevokeFamily(_ context.Context, familyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revokeFamilyLocked(familyID, time.Now())
	return nil
}

func (m *memStore) RevokeSubject(_ context.Context, subject string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for _, h := range m.subjects[subject] {
		if r, ok := m.records[h]; ok && r.RevokedAt == nil {
			t := now
			r.RevokedAt = &t
		}
	}
	return nil
}

func (m *memStore) GarbageCollect(_ context.Context, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for h, r := range m.records {
		if !r.ExpiresAt.After(now) {
			delete(m.records, h)
			n++
		}
	}
	return n, nil
}

func (m *memStore) revokeFamilyLocked(familyID string, now time.Time) {
	for _, h := range m.families[familyID] {
		if r, ok := m.records[h]; ok && r.RevokedAt == nil {
			t := now
			r.RevokedAt = &t
		}
	}
}
