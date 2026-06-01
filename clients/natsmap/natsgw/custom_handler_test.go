package natsgw_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/clients/natsmap/natsgw"
	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestCustomHandler_ReplacesDefaultPublish(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()

	var called int32
	app := mountApp(rt,
		natsgw.WithCustomHandler(func(_ context.Context, _ *fiber.Ctx,
			_ *natsmap.Runtime, _ string, _ []byte, _ map[string][]string) error {
			atomic.AddInt32(&called, 1)
			// Intentionally do NOT publish — verify the kit
			// doesn't double-publish on top of us.
			return nil
		}),
	)
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha", bytes.NewReader([]byte(`{}`)))
	resp, _ := app.Test(req)
	if resp.StatusCode != fiber.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&called); got != 1 {
		t.Errorf("custom handler called %d times, want 1", got)
	}
}

func TestCustomHandler_ReceivesPipelineState(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()

	var (
		gotSubject string
		gotBody    []byte
		gotHeader  string
	)
	app := mountApp(rt,
		natsgw.WithHeaderForwarder("X-Tenant"),
		natsgw.WithCustomHandler(func(_ context.Context, _ *fiber.Ctx,
			_ *natsmap.Runtime, sub string, body []byte, h map[string][]string) error {
			gotSubject = sub
			gotBody = append(gotBody, body...)
			if v := h["X-Tenant"]; len(v) > 0 {
				gotHeader = v[0]
			}
			return nil
		}),
	)
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha",
		bytes.NewReader([]byte(`{"hello":"world"}`)))
	req.Header.Set("X-Tenant", "acme")
	_, _ = app.Test(req)

	if gotSubject != "gwtest.alpha" {
		t.Errorf("subject = %q, want gwtest.alpha", gotSubject)
	}
	if string(gotBody) != `{"hello":"world"}` {
		t.Errorf("body = %q, want round-trip", gotBody)
	}
	if gotHeader != "acme" {
		t.Errorf("X-Tenant = %q, want acme", gotHeader)
	}
}

func TestCustomHandler_ReturnsCustomError(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()
	app := mountApp(rt,
		natsgw.WithCustomHandler(func(_ context.Context, _ *fiber.Ctx,
			_ *natsmap.Runtime, _ string, _ []byte, _ map[string][]string) error {
			return xerrs.Validation("custom_reject", "I rejected this")
		}),
	)
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha", bytes.NewReader([]byte(`{}`)))
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "custom_reject") {
		t.Errorf("custom Code not surfaced: %s", body)
	}
}

func TestCustomHandler_PlainErrorFallsThroughToErrorHandler(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()
	app := mountApp(rt,
		natsgw.WithCustomHandler(func(_ context.Context, _ *fiber.Ctx,
			_ *natsmap.Runtime, _ string, _ []byte, _ map[string][]string) error {
			return errors.New("plain go error")
		}),
	)
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha", bytes.NewReader([]byte(`{}`)))
	resp, _ := app.Test(req)
	if resp.StatusCode != 500 {
		t.Errorf("status = %d, want 500 (plain error)", resp.StatusCode)
	}
}

func TestCustomHandler_CustomResponseBodyHonoured(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()
	app := mountApp(rt,
		natsgw.WithCustomHandler(func(_ context.Context, fc *fiber.Ctx,
			_ *natsmap.Runtime, _ string, _ []byte, _ map[string][]string) error {
			return fc.Status(fiber.StatusCreated).JSON(map[string]string{
				"id": "durable-42",
			})
		}),
	)
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha", bytes.NewReader([]byte(`{}`)))
	resp, _ := app.Test(req)
	if resp.StatusCode != fiber.StatusCreated {
		t.Errorf("status = %d, want 201 (custom)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "durable-42") {
		t.Errorf("custom body not preserved: %s", body)
	}
}

func TestCustomHandler_CanPublishViaRuntime(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()
	natsc, _ := natsclient.Connect(context.Background(), natsclient.Config{URL: testURL})
	defer natsc.Close()
	ncSub, _ := natsc.Conn().SubscribeSync("gwtest.alpha")

	app := mountApp(rt,
		natsgw.WithCustomHandler(func(ctx context.Context, _ *fiber.Ctx,
			rt *natsmap.Runtime, sub string, body []byte, h map[string][]string) error {
			// Custom handler tee-ing: publish AND ack. Here we
			// just publish via the kit primitive.
			return natsmap.PublishRaw(ctx, rt, sub, body, h)
		}),
	)
	body := []byte(`{"customhandler":"published"}`)
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha", bytes.NewReader(body))
	resp, _ := app.Test(req)
	if resp.StatusCode != fiber.StatusAccepted {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	msg, err := ncSub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("NextMsg: %v", err)
	}
	if string(msg.Data) != string(body) {
		t.Errorf("payload = %q, want %q", msg.Data, body)
	}
}

func TestCustomHandler_RunsAfterValidator(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()
	var customCalled int32
	app := mountApp(rt,
		natsgw.WithValidator(func(_ context.Context, _ string, _ []byte) error {
			return errors.New("validator says no")
		}),
		natsgw.WithCustomHandler(func(_ context.Context, _ *fiber.Ctx,
			_ *natsmap.Runtime, _ string, _ []byte, _ map[string][]string) error {
			atomic.AddInt32(&customCalled, 1)
			return nil
		}),
	)
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha", bytes.NewReader([]byte(`{}`)))
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (validator rejected)", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&customCalled); got != 0 {
		t.Errorf("custom handler called %d times, want 0 (validator-fail short-circuits)", got)
	}
}
