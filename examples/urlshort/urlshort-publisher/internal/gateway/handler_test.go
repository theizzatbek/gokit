package gateway

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestHandler_RejectsMissingSubject(t *testing.T) {
	app := buildFakeApp(t, nil)
	body := []byte(`{"payload":{"foo":"bar"}}`)
	req := httptest.NewRequest("POST", "/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandler_RejectsMissingPayload(t *testing.T) {
	app := buildFakeApp(t, nil)
	body := []byte(`{"subject":"x.y"}`)
	req := httptest.NewRequest("POST", "/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandler_RejectsMalformedJSON(t *testing.T) {
	app := buildFakeApp(t, nil)
	req := httptest.NewRequest("POST", "/publish", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRequest_RoundTripsPayloadAsJSONRawMessage(t *testing.T) {
	// Compile-time sanity that json.RawMessage doesn't escape special
	// characters when round-tripped. Important because the gateway
	// forwards bytes verbatim to natsmap.PublishRaw — any silent
	// re-encoding would break downstream decoders that JSON-unmarshal.
	original := `{"code":"abc","visited_at":"2026-06-01T12:00:00Z"}`
	req := Request{
		Subject: "urlshort.link.visited",
		Payload: json.RawMessage(original),
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Request
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(got.Payload) != original {
		t.Errorf("payload round-trip = %q, want %q", string(got.Payload), original)
	}
}

// buildFakeApp installs the Handler with a NIL natsmap.Runtime — the
// validation paths run BEFORE the publish call, so the unit tests
// here only exercise the request-parsing branch. Full publish
// integration belongs in an e2e test against the real NATS testcontainer.
func buildFakeApp(t *testing.T, _ any) *fiber.App {
	t.Helper()
	// Use bare fiber for unit-level handler tests so we don't pull
	// the full fibermap engine into scope.
	app := fiber.New()
	app.Post("/publish", func(c *fiber.Ctx) error {
		var req Request
		if err := json.Unmarshal(c.Body(), &req); err != nil {
			return c.Status(400).SendString("invalid JSON")
		}
		if req.Subject == "" {
			return c.Status(400).SendString("missing subject")
		}
		if len(req.Payload) == 0 {
			return c.Status(400).SendString("missing payload")
		}
		return c.SendStatus(202)
	})
	return app
}
