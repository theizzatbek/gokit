package sessions_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/sessions"
)

func counterValue(t *testing.T, reg *prometheus.Registry, name, op, outcome string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.Metric {
			lm := map[string]string{}
			for _, l := range m.GetLabel() {
				lm[l.GetName()] = l.GetValue()
			}
			if lm["op"] == op && (outcome == "" || lm["outcome"] == outcome) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// labelMapHelper avoids unused-warning for dto when only counterValue is used.
var _ = (*dto.Metric)(nil)

type tClaims struct {
	Plan string `json:"plan"`
}

func buildAuth(t *testing.T) *auth.Auth[tClaims] {
	t.Helper()
	keys, err := auth.GenerateEd25519Key("k1")
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.New[tClaims](auth.Config{
		Issuer: "test", Keys: keys,
		AccessTTL: time.Minute, RefreshTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestManager_Metrics_IssueAndMiddleware(t *testing.T) {
	a := buildAuth(t)
	store := sessions.NewMemoryStore()
	reg := prometheus.NewRegistry()
	sm, err := a.Sessions(sessions.Config{
		Store:          store,
		TTL:            time.Hour,
		IdleTimeout:    15 * time.Minute,
		InsecureCookie: true,
	}, sessions.WithMetrics(reg))
	if err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	app.Post("/login", func(c *fiber.Ctx) error {
		return sm.Issue(c, "u-1", tClaims{Plan: "pro"}, nil, nil)
	})
	app.Get("/me", sm.Middleware(sessions.Required), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	resp, _ := app.Test(httptest.NewRequest("POST", "/login", nil))
	if resp.StatusCode != 200 {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	if got := counterValue(t, reg, "sessions_ops_total", "issue", "ok"); got != 1 {
		t.Errorf("issue/ok = %v, want 1", got)
	}

	cookies := resp.Cookies()
	req := httptest.NewRequest("GET", "/me", nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	resp2, _ := app.Test(req)
	if resp2.StatusCode != 200 {
		t.Fatalf("me status = %d", resp2.StatusCode)
	}
	if got := counterValue(t, reg, "sessions_ops_total", "middleware", "ok"); got != 1 {
		t.Errorf("middleware/ok = %v, want 1", got)
	}

	// Anonymous request to a Required route → missing.
	resp3, _ := app.Test(httptest.NewRequest("GET", "/me", nil))
	if resp3.StatusCode == 200 {
		t.Errorf("anonymous got 200, want 4xx/5xx")
	}
	if got := counterValue(t, reg, "sessions_ops_total", "middleware", "missing"); got != 1 {
		t.Errorf("middleware/missing = %v, want 1", got)
	}
}

func TestManager_Metrics_ExpiredOutcome(t *testing.T) {
	a := buildAuth(t)
	store := sessions.NewMemoryStore()
	reg := prometheus.NewRegistry()
	sm, err := a.Sessions(sessions.Config{
		Store:          store,
		TTL:            50 * time.Millisecond,
		IdleTimeout:    50 * time.Millisecond,
		InsecureCookie: true,
	}, sessions.WithMetrics(reg))
	if err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	app.Post("/login", func(c *fiber.Ctx) error {
		return sm.Issue(c, "u-x", tClaims{}, nil, nil)
	})
	app.Get("/me", sm.Middleware(sessions.Required), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	resp, _ := app.Test(httptest.NewRequest("POST", "/login", nil))
	cookies := resp.Cookies()

	time.Sleep(70 * time.Millisecond)

	req := httptest.NewRequest("GET", "/me", nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	if _, err := app.Test(req); err != nil {
		t.Fatal(err)
	}
	if got := counterValue(t, reg, "sessions_ops_total", "middleware", "expired"); got != 1 {
		t.Errorf("middleware/expired = %v, want 1", got)
	}
}

func TestManager_OnIssueAndOnLogout_Fire(t *testing.T) {
	a := buildAuth(t)
	store := sessions.NewMemoryStore()
	var (
		issued      atomic.Int32
		loggedOut   atomic.Int32
		lastSubject string
	)
	sm, err := a.Sessions(sessions.Config{
		Store:          store,
		TTL:            time.Hour,
		InsecureCookie: true,
	},
		sessions.WithOnIssue(func(_ context.Context, sess *sessions.Session) {
			issued.Add(1)
			lastSubject = sess.Subject
		}),
		sessions.WithOnLogout(func(_ context.Context, _, subject string) {
			loggedOut.Add(1)
			lastSubject = subject
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	app.Post("/login", func(c *fiber.Ctx) error {
		return sm.Issue(c, "u-h", tClaims{}, nil, nil)
	})
	app.Post("/logout", func(c *fiber.Ctx) error {
		return sm.Logout(c)
	})
	resp, _ := app.Test(httptest.NewRequest("POST", "/login", nil))
	if issued.Load() != 1 || lastSubject != "u-h" {
		t.Errorf("issued=%d subject=%q", issued.Load(), lastSubject)
	}
	req := httptest.NewRequest("POST", "/logout", nil)
	for _, ck := range resp.Cookies() {
		req.AddCookie(ck)
	}
	if _, err := app.Test(req); err != nil {
		t.Fatal(err)
	}
	if loggedOut.Load() != 1 || lastSubject != "u-h" {
		t.Errorf("loggedOut=%d subject=%q", loggedOut.Load(), lastSubject)
	}
}

func TestManager_OnLogoutEverywhere_FiresWithCount(t *testing.T) {
	a := buildAuth(t)
	store := sessions.NewMemoryStore()
	var gotCount atomic.Int32
	sm, err := a.Sessions(sessions.Config{
		Store: store, TTL: time.Hour, InsecureCookie: true,
	},
		sessions.WithOnLogoutEverywhere(func(_ context.Context, _ string, count int) {
			gotCount.Store(int32(count))
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Post("/login", func(c *fiber.Ctx) error {
		return sm.Issue(c, "u-bulk", tClaims{}, nil, nil)
	})
	// 3 separate sessions for the same subject.
	for i := 0; i < 3; i++ {
		if _, err := app.Test(httptest.NewRequest("POST", "/login", nil)); err != nil {
			t.Fatal(err)
		}
	}
	if err := sm.LogoutEverywhere(context.Background(), "u-bulk"); err != nil {
		t.Fatal(err)
	}
	if gotCount.Load() != 3 {
		t.Errorf("count = %d, want 3", gotCount.Load())
	}
}

func TestManager_OnExpire_FiresOnInLineExpire(t *testing.T) {
	a := buildAuth(t)
	store := sessions.NewMemoryStore()
	var expired atomic.Int32
	sm, err := a.Sessions(sessions.Config{
		Store:          store,
		TTL:            50 * time.Millisecond,
		IdleTimeout:    50 * time.Millisecond,
		InsecureCookie: true,
	},
		sessions.WithOnExpire(func(_ context.Context, _, _ string) { expired.Add(1) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Post("/login", func(c *fiber.Ctx) error {
		return sm.Issue(c, "u-exp", tClaims{}, nil, nil)
	})
	app.Get("/me", sm.Middleware(sessions.Required), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	resp, _ := app.Test(httptest.NewRequest("POST", "/login", nil))
	time.Sleep(80 * time.Millisecond)
	req := httptest.NewRequest("GET", "/me", nil)
	for _, ck := range resp.Cookies() {
		req.AddCookie(ck)
	}
	if _, err := app.Test(req); err != nil {
		t.Fatal(err)
	}
	if expired.Load() != 1 {
		t.Errorf("expired = %d, want 1", expired.Load())
	}
}

func TestManager_Hooks_PanicRecovered(t *testing.T) {
	a := buildAuth(t)
	store := sessions.NewMemoryStore()
	logbuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logbuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sm, err := a.Sessions(sessions.Config{
		Store: store, TTL: time.Hour, InsecureCookie: true,
	},
		sessions.WithLogger(logger),
		sessions.WithOnIssue(func(context.Context, *sessions.Session) { panic("boom") }),
	)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Post("/login", func(c *fiber.Ctx) error {
		return sm.Issue(c, "u-p", tClaims{}, nil, nil)
	})
	resp, _ := app.Test(httptest.NewRequest("POST", "/login", nil))
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 (hook panic should not abort issue)", resp.StatusCode)
	}
	if !bytes.Contains(logbuf.Bytes(), []byte("OnIssue panic recovered")) {
		t.Errorf("logger missing panic record; logs=%q", logbuf.String())
	}
}

func TestManager_RevokeByID_FiresLogoutHook(t *testing.T) {
	a := buildAuth(t)
	store := sessions.NewMemoryStore()
	var revoked atomic.Int32
	sm, err := a.Sessions(sessions.Config{
		Store: store, TTL: time.Hour, InsecureCookie: true,
	},
		sessions.WithOnLogout(func(context.Context, string, string) { revoked.Add(1) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Post("/login", func(c *fiber.Ctx) error {
		return sm.Issue(c, "u-r", tClaims{}, nil, nil)
	})
	resp, _ := app.Test(httptest.NewRequest("POST", "/login", nil))

	// Pull the issued session ID from the cookie.
	var sessID string
	for _, ck := range resp.Cookies() {
		if ck.Name == "sid" {
			sessID = ck.Value
		}
	}
	if sessID == "" {
		t.Fatal("no sid cookie")
	}
	if err := sm.RevokeByID(context.Background(), sessID); err != nil {
		t.Fatal(err)
	}
	if revoked.Load() != 1 {
		t.Errorf("hook fires = %d, want 1", revoked.Load())
	}
	// Idempotent — second call still ok, hook fires again (subject empty).
	if err := sm.RevokeByID(context.Background(), sessID); err != nil {
		t.Fatal(err)
	}
	// Empty id short-circuits, hook NOT fired.
	before := revoked.Load()
	if err := sm.RevokeByID(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if revoked.Load() != before {
		t.Errorf("empty id should not fire hook; fired = %d (was %d)", revoked.Load(), before)
	}
}

func TestMemoryStore_Lister(t *testing.T) {
	store := sessions.NewMemoryStore()
	ctx := context.Background()
	now := time.Now()
	mk := func(id, subject string, expires time.Time) *sessions.Session {
		return &sessions.Session{
			ID: id, Subject: subject,
			CreatedAt: now, LastSeenAt: now, ExpiresAt: expires,
		}
	}
	// 2 active for u-1, 1 expired for u-1, 1 active for u-2.
	_ = store.Create(ctx, mk("a", "u-1", now.Add(time.Hour)))
	time.Sleep(time.Millisecond) // ensure created_at differs
	_ = store.Create(ctx, mk("b", "u-1", now.Add(time.Hour)))
	_ = store.Create(ctx, mk("c", "u-1", now.Add(-time.Hour)))
	_ = store.Create(ctx, mk("d", "u-2", now.Add(time.Hour)))

	rows, err := store.ListBySubject(ctx, "u-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Errorf("rows = %d, want 3", len(rows))
	}
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Active != 3 || stats.Total != 4 {
		t.Errorf("stats = %+v, want active=3 total=4", stats)
	}
	// Empty subject.
	empty, _ := store.ListBySubject(ctx, "")
	if len(empty) != 0 {
		t.Errorf("empty subject rows = %d", len(empty))
	}
}
