package natsgw_test

import (
	"bytes"
	"context"
	"flag"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/clients/natsmap/natsgw"
	"github.com/theizzatbek/gokit/fibermap"
)

var testURL string

func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		return 0
	}
	ctx := context.Background()
	c, err := tcnats.Run(ctx, "nats:2-alpine", testcontainers.WithCmd("-js"))
	if err != nil {
		println("testcontainers nats start failed:", err.Error())
		return 1
	}
	defer testcontainers.TerminateContainer(c)
	endpoint, err := c.ConnectionString(ctx)
	if err != nil {
		println("nats endpoint:", err.Error())
		return 1
	}
	testURL = endpoint
	return m.Run()
}

// streamOnce ensures the shared stream covering gwtest.> exists.
// Per-test stream creation would race on "subjects overlap" since
// JetStream rejects multiple streams claiming the same subject
// hierarchy.
var streamOnce sync.Once

func ensureSharedStream(t *testing.T, c *natsclient.Client) {
	t.Helper()
	streamOnce.Do(func() {
		if err := c.EnsureStream(context.Background(), natsclient.StreamConfig{
			Name: "NATSGW_SHARED", Subjects: []string{"gwtest.>"},
		}); err != nil {
			t.Fatalf("EnsureStream: %v", err)
		}
	})
}

func buildRuntime(t *testing.T, subjects ...string) (*natsmap.Runtime, func()) {
	t.Helper()
	if testing.Short() || testURL == "" {
		t.Skip("requires NATS testcontainer")
	}
	ctx := context.Background()
	c, err := natsclient.Connect(ctx, natsclient.Config{URL: testURL})
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	ensureSharedStream(t, c)

	eng := natsmap.New()
	type payload struct{}
	for _, s := range subjects {
		natsmap.RegisterPublisher[payload](eng, s)
	}
	// Engine.Build requires every loaded publisher to have a
	// registered type. We register the same minimum payload for
	// every test subject.
	yaml := "publishers:\n"
	for _, s := range subjects {
		yaml += "  - name: " + s + "\n    subject: " + s + "\n"
	}
	if err := eng.LoadBytes([]byte(yaml)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	rt, err := eng.Build(ctx, c)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return rt, func() {
		_ = rt.Drain()
		c.Close()
	}
}

func mountApp(rt *natsmap.Runtime, opts ...natsgw.Option) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	app.Post("/publish/:subject", natsgw.Handler(rt, opts...))
	return app
}

func TestHandler_PublishesBodyToSubject(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()

	// Subscribe via plain core NATS for read-back.
	natsc, err := natsclient.Connect(context.Background(), natsclient.Config{URL: testURL})
	if err != nil {
		t.Fatalf("subscriber connect: %v", err)
	}
	defer natsc.Close()
	ncSub, err := natsc.Conn().SubscribeSync("gwtest.alpha")
	if err != nil {
		t.Fatalf("SubscribeSync: %v", err)
	}

	app := mountApp(rt)
	body := []byte(`{"hello":"world"}`)
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha", bytes.NewReader(body))
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	msg, err := ncSub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("NextMsg: %v", err)
	}
	if string(msg.Data) != string(body) {
		t.Errorf("payload = %q, want %q", msg.Data, body)
	}
}

func TestHandler_MissingSubjectIs400(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()
	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	// No :subject param — uses default extractor on empty path param.
	app.Post("/publish", natsgw.Handler(rt))
	resp, _ := app.Test(httptest.NewRequest("POST", "/publish", bytes.NewReader([]byte(`{}`))))
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandler_AllowlistRejectsUnknown(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha", "gwtest.beta")
	defer cleanup()
	app := mountApp(rt, natsgw.WithSubjectAllowlist("gwtest.alpha"))

	req := httptest.NewRequest("POST", "/publish/gwtest.beta", bytes.NewReader([]byte(`{}`)))
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (subject not allowed)", resp.StatusCode)
	}
}

func TestHandler_BodyLimitRejected(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()
	app := mountApp(rt, natsgw.WithMaxBodySize(10))

	body := bytes.Repeat([]byte("x"), 100)
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha", bytes.NewReader(body))
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (body too large)", resp.StatusCode)
	}
}

func TestHandler_UnknownPublisherIs503(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()
	app := mountApp(rt)

	req := httptest.NewRequest("POST", "/publish/gwtest.unknown",
		bytes.NewReader([]byte(`{}`)))
	resp, _ := app.Test(req)
	// natsmap returns KindNotFound on unknown publisher → 404 via errs.HTTP.
	if resp.StatusCode != 404 && resp.StatusCode != 503 {
		t.Errorf("status = %d, want 404 or 503 (unknown publisher)", resp.StatusCode)
	}
}

func TestHandler_ForwardsHeaders(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()
	natsc, _ := natsclient.Connect(context.Background(), natsclient.Config{URL: testURL})
	defer natsc.Close()
	ncSub, _ := natsc.Conn().SubscribeSync("gwtest.alpha")

	app := mountApp(rt, natsgw.WithHeaderForwarder("X-Tenant"))
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha",
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Tenant", "acme")
	resp, _ := app.Test(req)
	if resp.StatusCode != fiber.StatusAccepted {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	msg, err := ncSub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("NextMsg: %v", err)
	}
	if got := msg.Header.Get("X-Tenant"); got != "acme" {
		t.Errorf("X-Tenant header = %q, want acme", got)
	}
}

func TestHandler_CustomStatusCode(t *testing.T) {
	rt, cleanup := buildRuntime(t, "gwtest.alpha")
	defer cleanup()
	app := mountApp(rt, natsgw.WithStatusOK(fiber.StatusNoContent))
	req := httptest.NewRequest("POST", "/publish/gwtest.alpha",
		bytes.NewReader([]byte(`{}`)))
	resp, _ := app.Test(req)
	if resp.StatusCode != fiber.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
}

// silence strings import (referenced only by removed code path).
var _ = strings.Contains
