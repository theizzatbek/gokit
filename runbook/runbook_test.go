package runbook_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/audit"
	"github.com/theizzatbek/gokit/runbook"
)

func TestNew_NilStoreErrors(t *testing.T) {
	if _, err := runbook.New(nil); err == nil {
		t.Fatal("expected error for nil Store")
	}
}

func TestEnabled_DefaultOn(t *testing.T) {
	r, _ := runbook.New(runbook.NewMemoryStore())
	if !r.Enabled(context.Background(), "anything") {
		t.Error("default-on contract broken: missing flag returned false")
	}
}

func TestSetEnabled_PersistsAndCaches(t *testing.T) {
	store := runbook.NewMemoryStore()
	r, _ := runbook.New(store)
	err := r.SetEnabled(context.Background(), "checkout", false, audit.Actor{Subject: "ops-1"})
	if err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if r.Enabled(context.Background(), "checkout") {
		t.Error("Enabled returned true after SetEnabled(false)")
	}
	// Store also reflects.
	got, ok, _ := store.Get(context.Background(), "checkout")
	if !ok || got {
		t.Errorf("store.Get = (%v, %v); want (false, true)", got, ok)
	}
}

func TestSetEnabled_InvalidNameErrors(t *testing.T) {
	r, _ := runbook.New(runbook.NewMemoryStore())
	err := r.SetEnabled(context.Background(), "Invalid Name!", true, audit.Actor{})
	if err == nil {
		t.Fatal("expected error for invalid flag name")
	}
}

func TestSetEnabled_EmitsAuditEvent(t *testing.T) {
	auditStore := audit.NewMemoryStore()
	al, _ := audit.New(auditStore, audit.Config{ServiceName: "test"})
	r, _ := runbook.New(runbook.NewMemoryStore(), runbook.WithAuditor(al))

	_ = r.SetEnabled(context.Background(), "checkout", false, audit.Actor{Subject: "ops-1"})
	events := auditStore.Snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	if events[0].Action != "runbook.flag_changed" {
		t.Errorf("action = %q", events[0].Action)
	}
	if events[0].Target.ID != "checkout" {
		t.Errorf("target.ID = %q", events[0].Target.ID)
	}
	if events[0].Actor.Subject != "ops-1" {
		t.Errorf("actor.Subject = %q", events[0].Actor.Subject)
	}
}

func TestRefresh_PicksUpOutOfBandStoreChanges(t *testing.T) {
	store := runbook.NewMemoryStore()
	// Simulate another pod writing to the store BEFORE this Runbook
	// constructs its cache.
	_ = store.Set(context.Background(), "checkout", false)

	r, _ := runbook.New(store, runbook.WithRefreshInterval(50*time.Millisecond))
	defer r.Close()

	if r.Enabled(context.Background(), "checkout") {
		t.Error("initial snapshot should pick up pre-existing flag")
	}

	// Now flip via store (simulates another pod).
	_ = store.Set(context.Background(), "checkout", true)
	// Wait for refresh.
	time.Sleep(150 * time.Millisecond)
	if !r.Enabled(context.Background(), "checkout") {
		t.Error("refresh loop did not pick up store change")
	}
}

func TestMount_HTMLLists(t *testing.T) {
	r, _ := runbook.New(runbook.NewMemoryStore())
	_ = r.SetEnabled(context.Background(), "checkout", false, audit.Actor{})
	app := fiber.New()
	runbook.Mount(app, "/_kit/runbook", r)

	resp, _ := app.Test(httptest.NewRequest("GET", "/_kit/runbook", nil))
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, "Runbook") {
		t.Errorf("HTML missing title")
	}
	if !strings.Contains(s, "checkout") {
		t.Errorf("flag not rendered")
	}
	if !strings.Contains(s, "DISABLED") {
		t.Errorf("state not rendered")
	}
}

func TestMount_POSTFlipsFlag(t *testing.T) {
	r, _ := runbook.New(runbook.NewMemoryStore())
	app := fiber.New()
	runbook.Mount(app, "/_kit/runbook", r,
		runbook.WithSubjectFn(func(c *fiber.Ctx) string { return c.Get("X-User") }))

	body := bytes.NewBufferString(`{"enabled": false}`)
	req := httptest.NewRequest("POST", "/_kit/runbook/checkout", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User", "ops-1")
	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if r.Enabled(context.Background(), "checkout") {
		t.Error("flag still enabled after POST")
	}
}

func TestMount_JSONSnapshot(t *testing.T) {
	r, _ := runbook.New(runbook.NewMemoryStore())
	_ = r.SetEnabled(context.Background(), "checkout", true, audit.Actor{})
	_ = r.SetEnabled(context.Background(), "billing", false, audit.Actor{})

	app := fiber.New()
	runbook.Mount(app, "/_kit/runbook", r)
	resp, _ := app.Test(httptest.NewRequest("GET", "/_kit/runbook.json", nil))
	body, _ := io.ReadAll(resp.Body)
	var got map[string]bool
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["checkout"] != true || got["billing"] != false {
		t.Errorf("snapshot mismatch: %+v", got)
	}
}
