package auth_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/internal/memstore"
)

// captureSecurityLog wires a JSON-line slog handler over a buffer so
// tests can assert that named events (and their attributes) reached
// the security log.
func captureSecurityLog(t *testing.T) (*auth.Auth[appClaims], *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	keys, _ := auth.GenerateEd25519Key("k1")
	a, err := auth.New[appClaims](auth.Config{
		Issuer: "myapp", Audience: []string{"web"},
		Keys: keys, AccessTTL: 15 * time.Minute, RefreshTTL: 30 * 24 * time.Hour,
	},
		auth.WithRefreshStore(memstore.New()),
		auth.WithCookieSecure(false),
		auth.WithSecurityLogger(logger),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a, buf
}

// collectEvents parses the buffered JSON-lines and returns the
// slice of decoded events.
func collectEvents(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad json line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestSecurityLog_LoginSuccessEmitted(t *testing.T) {
	a, buf := captureSecurityLog(t)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Post("/auth/login", func(c *fiber.Ctx) error {
		return a.IssueLogin(c, auth.LoginResult[appClaims]{Subject: "u-42"})
	})

	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TestAgent/1.0")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	events := collectEvents(t, buf)
	if len(events) == 0 {
		t.Fatalf("no security events emitted, buf = %q", buf.String())
	}
	var login map[string]any
	for _, e := range events {
		if e["msg"] == "login_success" {
			login = e
		}
	}
	if login == nil {
		t.Fatalf("login_success event missing, got %v", events)
	}
	if sub, _ := login["subject"].(string); sub != "u-42" {
		t.Errorf("subject = %v, want u-42", login["subject"])
	}
	if ua, _ := login["ua"].(string); ua != "TestAgent/1.0" {
		t.Errorf("ua = %v, want TestAgent/1.0", login["ua"])
	}
	if level, _ := login["level"].(string); level != "INFO" {
		t.Errorf("level = %v, want INFO", login["level"])
	}
}

func TestSecurityLog_LogoutEmitsWithSubject(t *testing.T) {
	a, buf := captureSecurityLog(t)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Post("/auth/login", func(c *fiber.Ctx) error {
		return a.IssueLogin(c, auth.LoginResult[appClaims]{Subject: "u-9"})
	})
	app.Post("/auth/logout", a.Logout)

	// Login → grab refresh cookie.
	loginReq := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, _ := app.Test(loginReq)
	loginResp.Body.Close()
	var refresh *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == "refresh_token" {
			refresh = c
		}
	}
	if refresh == nil {
		t.Fatal("no refresh cookie")
	}
	buf.Reset() // drop the login_success line; only care about logout in this assertion

	logoutReq := httptest.NewRequest("POST", "/auth/logout", nil)
	logoutReq.AddCookie(refresh)
	if _, err := app.Test(logoutReq); err != nil {
		t.Fatal(err)
	}

	events := collectEvents(t, buf)
	if len(events) != 1 || events[0]["msg"] != "logout" {
		t.Fatalf("events = %v, want one logout entry", events)
	}
	if sub, _ := events[0]["subject"].(string); sub != "u-9" {
		t.Errorf("subject = %v, want u-9", events[0]["subject"])
	}
}

func TestSecurityLog_LogoutNoCookie_NoEvent(t *testing.T) {
	a, buf := captureSecurityLog(t)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Post("/auth/logout", a.Logout)

	if _, err := app.Test(httptest.NewRequest("POST", "/auth/logout", nil)); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no events for cookieless logout, got %q", buf.String())
	}
}

func TestSecurityLog_SilentWhenLoggerNotWired(t *testing.T) {
	// Build Auth WITHOUT WithSecurityLogger — security calls must be no-ops.
	keys, _ := auth.GenerateEd25519Key("k1")
	a, _ := auth.New[appClaims](auth.Config{
		Issuer: "myapp", Audience: []string{"web"},
		Keys: keys, AccessTTL: 15 * time.Minute, RefreshTTL: 30 * 24 * time.Hour,
	}, auth.WithRefreshStore(memstore.New()), auth.WithCookieSecure(false))

	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Post("/auth/login", func(c *fiber.Ctx) error {
		return a.IssueLogin(c, auth.LoginResult[appClaims]{Subject: "u-1"})
	})
	resp, err := app.Test(httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// No assertion on logs — just ensure no panic / nil-deref when securityLogger is nil.
}
