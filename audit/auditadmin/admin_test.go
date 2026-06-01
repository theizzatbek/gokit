package auditadmin_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/audit"
	"github.com/theizzatbek/gokit/audit/auditadmin"
)

func seed(t *testing.T) (*audit.Logger, *audit.MemoryStore) {
	t.Helper()
	store := audit.NewMemoryStore()
	l, _ := audit.New(store, audit.Config{ServiceName: "tasks"})
	now := time.Now()
	_, _ = l.Log(context.Background(), audit.Event{
		OccurredAt: now.Add(-2 * time.Hour),
		Action:     "user.created", Outcome: audit.Success,
		Actor:  audit.Actor{Subject: "u-1", IP: "10.0.0.1"},
		Target: audit.Target{Type: "user", ID: "u-99"},
	})
	_, _ = l.Log(context.Background(), audit.Event{
		OccurredAt: now.Add(-time.Hour),
		Action:     "user.deleted", Outcome: audit.Denied,
		Actor:    audit.Actor{Subject: "u-1"},
		Target:   audit.Target{Type: "user", ID: "u-99"},
		Metadata: map[string]any{"reason": "not_owner"},
	})
	_, _ = l.Log(context.Background(), audit.Event{
		OccurredAt: now,
		Action:     "post.created", Outcome: audit.Success,
		Actor: audit.Actor{Subject: "u-2"},
	})
	return l, store
}

func TestMount_HTMLRenders(t *testing.T) {
	logger, _ := seed(t)
	app := fiber.New()
	auditadmin.Mount(app, "/admin/audit", logger)

	resp, err := app.Test(httptest.NewRequest("GET", "/admin/audit", nil))
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, "<title>audit</title>") {
		t.Errorf("HTML missing title")
	}
	if !strings.Contains(s, "user.created") {
		t.Errorf("event 'user.created' not rendered")
	}
	if !strings.Contains(s, "u-1") {
		t.Errorf("actor not rendered")
	}
}

func TestMount_JSONExportReturnsFilteredSet(t *testing.T) {
	logger, _ := seed(t)
	app := fiber.New()
	auditadmin.Mount(app, "/admin/audit", logger)

	req := httptest.NewRequest("GET", "/admin/audit.json?actor=u-1", nil)
	resp, _ := app.Test(req)
	body, _ := io.ReadAll(resp.Body)
	var events []audit.Event
	if err := json.Unmarshal(body, &events); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if len(events) != 2 {
		t.Errorf("filtered len = %d, want 2 (u-1 has 2 events)", len(events))
	}
	for _, e := range events {
		if e.Actor.Subject != "u-1" {
			t.Errorf("Actor.Subject = %q, want u-1", e.Actor.Subject)
		}
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
}

func TestMount_InvalidQueryReturns400(t *testing.T) {
	logger, _ := seed(t)
	app := fiber.New()
	auditadmin.Mount(app, "/admin/audit", logger)

	req := httptest.NewRequest("GET", "/admin/audit?limit=abc", nil)
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 for bad limit", resp.StatusCode)
	}
}

func TestMount_WildcardActionFilter(t *testing.T) {
	logger, _ := seed(t)
	app := fiber.New()
	auditadmin.Mount(app, "/admin/audit", logger)

	req := httptest.NewRequest("GET", "/admin/audit.json?action=user.*", nil)
	resp, _ := app.Test(req)
	body, _ := io.ReadAll(resp.Body)
	var events []audit.Event
	_ = json.Unmarshal(body, &events)
	if len(events) != 2 {
		t.Errorf("wildcard match = %d, want 2", len(events))
	}
}

func TestMount_OutcomeFilter(t *testing.T) {
	logger, _ := seed(t)
	app := fiber.New()
	auditadmin.Mount(app, "/admin/audit", logger)

	req := httptest.NewRequest("GET", "/admin/audit.json?outcome=denied", nil)
	resp, _ := app.Test(req)
	body, _ := io.ReadAll(resp.Body)
	var events []audit.Event
	_ = json.Unmarshal(body, &events)
	if len(events) != 1 {
		t.Errorf("denied count = %d, want 1", len(events))
	}
}
