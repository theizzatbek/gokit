package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/clients/apimap"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
)

type smokeAppCtx struct {
	UserID string
}
type smokeClaims struct {
	Email string `json:"email"`
}

// TestSmoke_AllSubsystems exercises service.New with DB, Auth, NATS,
// APIMap, HTTPC, Engine all enabled. Register → login → authenticated
// call works end-to-end via Engine.Mount onto a fiber.App.
func TestSmoke_AllSubsystems(t *testing.T) {
	if testing.Short() {
		t.Skip("smoke test requires Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	dbCfg := startSmokePostgres(t, ctx)
	natsURL := startSmokeNATS(t, ctx)
	upstream := startSmokeStub(t)
	pemKey := smokeEd25519PEM(t)
	apimapPath := writeSmokeClientsYAML(t)

	cfg := Config{
		DB:     dbCfg,
		Auth:   AuthConfig{PrivateKeyPEM: pemKey, KID: "k1", Issuer: "smoke", AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour},
		NATS:   NATSConfig{URL: natsURL},
		APIMap: APIMapConfig{Path: apimapPath},
	}
	cfg.Service.LogLevel = "error"

	svc, err := New[smokeAppCtx, smokeClaims](ctx, cfg,
		WithAPIMapEnv(map[string]string{"MICROLINK_BASE_URL": upstream.URL}),
		WithAPIMapRegistration(func(e *apimap.Engine) {
			apimap.RegisterResponse[map[string]any](e, "stub.get")
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)

	if svc.DB == nil || svc.Auth == nil || svc.NATS == nil || svc.APIMap == nil ||
		svc.HTTPC == nil || svc.Engine == nil || svc.Hasher == nil {
		t.Fatalf("expected every subsystem set, got DB=%v Auth=%v NATS=%v APIMap=%v HTTPC=%v Engine=%v Hasher=%v",
			svc.DB != nil, svc.Auth != nil, svc.NATS != nil, svc.APIMap != nil,
			svc.HTTPC != nil, svc.Engine != nil, svc.Hasher != nil)
	}

	// Apply refresh-token DDL so /auth/login can persist its token.
	if _, err := svc.DB.Exec(ctx, smokeRefreshDDL); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc.SetContextBuilder(func(c *fiber.Ctx) (smokeAppCtx, error) {
		return smokeAppCtx{UserID: svc.Auth.Subject(c)}, nil
	})
	svc.SetCredentialsVerifier(func(ctx context.Context, req auth.LoginRequest) (auth.LoginResult[smokeClaims], error) {
		if req.Login != "user" || req.Password != "pass" {
			return auth.LoginResult[smokeClaims]{}, xerrs.Unauthorized("invalid_credentials", "bad creds")
		}
		return auth.LoginResult[smokeClaims]{Subject: "subject-1", Custom: smokeClaims{Email: "user@example.com"}}, nil
	})

	fibermap.RegisterHandler(svc.Engine, "smoke.me",
		func(c *fibermap.Context[smokeAppCtx]) error {
			return c.JSON(map[string]string{"user_id": c.Data.UserID})
		})
	if err := svc.Engine.LoadBytes([]byte(smokeRoutesYAML)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(svc.Logger())})
	// Same Bearer-optional layer that Run installs via WithUse.
	app.Use(svc.Auth.Bearer(auth.BearerOptional))
	if err := svc.Engine.Mount(app); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	// /auth/login (auto-mounted by service.New) returns access_token.
	loginResp := doSmokeJSON(t, app, "POST", "/auth/login",
		`{"login":"user","password":"pass"}`, "")
	if loginResp.StatusCode != 200 {
		b, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("login: status %d body %s", loginResp.StatusCode, b)
	}
	var loginBody map[string]any
	_ = json.NewDecoder(loginResp.Body).Decode(&loginBody)
	token, _ := loginBody["access_token"].(string)
	if token == "" {
		t.Fatalf("no access_token: %v", loginBody)
	}

	// /me with Bearer returns the user_id from the JWT subject.
	meResp := doSmokeJSON(t, app, "GET", "/me", "", token)
	if meResp.StatusCode != 200 {
		b, _ := io.ReadAll(meResp.Body)
		t.Fatalf("me: status %d body %s", meResp.StatusCode, b)
	}
	var meBody map[string]any
	_ = json.NewDecoder(meResp.Body).Decode(&meBody)
	if uid, _ := meBody["user_id"].(string); uid != "subject-1" {
		t.Errorf("user_id = %q, want subject-1", meBody["user_id"])
	}

	// /me without Bearer is 401 (per-route bearer: [] enforces).
	noAuthResp := doSmokeJSON(t, app, "GET", "/me", "", "")
	if noAuthResp.StatusCode != 401 {
		t.Errorf("anonymous /me status = %d, want 401", noAuthResp.StatusCode)
	}
}

const smokeRefreshDDL = `
CREATE TABLE IF NOT EXISTS auth_refresh_tokens (
    token_hash  bytea       PRIMARY KEY,
    family_id   uuid        NOT NULL,
    parent_hash bytea       NOT NULL,
    subject     text        NOT NULL,
    issued_at   timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    revoked_at  timestamptz,
    user_agent  text        NOT NULL DEFAULT '',
    ip          inet
);
`

const smokeRoutesYAML = `
groups:
  - prefix: /
    middleware:
      - bearer: []
    routes:
      - method: GET
        path: /me
        handler: smoke.me
        name: smoke.me
`

func startSmokePostgres(t *testing.T, ctx context.Context) db.Config {
	t.Helper()
	c, err := tcpg.Run(ctx, "postgres:16-alpine",
		tcpg.WithDatabase("smoke"),
		tcpg.WithUsername("smoke"),
		tcpg.WithPassword("smoke"),
		tcpg.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("postgres testcontainer: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "5432/tcp")
	p, _ := strconv.Atoi(port.Port())
	return db.Config{Host: host, Port: p, User: "smoke", Password: "smoke", Database: "smoke", SSLMode: "disable"}
}

func startSmokeNATS(t *testing.T, ctx context.Context) string {
	t.Helper()
	c, err := tcnats.Run(ctx, "nats:2-alpine", testcontainers.WithCmd("-js"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, _ := c.ConnectionString(ctx)
	return url
}

func startSmokeStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeSmokeClientsYAML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "clients.yaml")
	body := `clients:
  - name: stub
    base_url: ${MICROLINK_BASE_URL}
    endpoints:
      - {name: get, method: GET, path: /, decode: json}
`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func smokeEd25519PEM(t *testing.T) string {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func doSmokeJSON(t *testing.T, app *fiber.App, method, path, body, bearer string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestService_NATSMapRoundTrip exercises service.New with only NATS + NATSMap
// configured. Publishing through svc.NATSMap delivers to a raw subscriber.
func TestService_NATSMapRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("smoke test requires Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	natsURL := startSmokeNATS(t, ctx)

	tmpDir := t.TempDir()
	pubPath := filepath.Join(tmpDir, "publishers.yaml")
	if err := os.WriteFile(pubPath, []byte(`publishers:
  - name: orders_out
    subject: svctest.orders
`), 0o644); err != nil {
		t.Fatalf("write publishers.yaml: %v", err)
	}

	cfg := Config{
		NATS:    NATSConfig{URL: natsURL, Name: "svctest"},
		NATSMap: NATSMapConfig{PublishersPath: pubPath},
	}
	cfg.Service.LogLevel = "error"

	svc, err := New[smokeAppCtx, smokeClaims](ctx, cfg,
		WithNATSMapRegistration(func(e *natsmap.Engine) {
			natsmap.RegisterPublisher[smokeOrder](e, "orders_out")
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)

	if svc.NATS == nil {
		t.Fatal("svc.NATS == nil")
	}
	if svc.NATSMap == nil {
		t.Fatal("svc.NATSMap == nil")
	}

	// Ensure the JetStream stream exists before publishing.
	if err := svc.NATS.EnsureStream(ctx, natsclient.StreamConfig{
		Name:     "SVCTEST",
		Subjects: []string{"svctest.>"},
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	// Raw subscribe (NOT through natsmap) to assert the published message
	// reaches the wire.
	recvCh := make(chan smokeOrder, 1)
	sub, err := natsclient.Subscribe[smokeOrder](ctx, svc.NATS, "svctest.orders",
		func(_ context.Context, m natsclient.Msg[smokeOrder]) error {
			select {
			case recvCh <- m.Data:
			default:
			}
			return nil
		}, natsclient.WithDurable("svctest-assert"))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Drain() })

	if err := natsmap.Publish[smokeOrder](ctx, svc.NATSMap, "orders_out",
		smokeOrder{ID: "o1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got := <-recvCh:
		if got.ID != "o1" {
			t.Fatalf("payload mismatch: got %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for delivery via svc.NATSMap")
	}

	// PublisherNames introspection.
	names := svc.NATSMap.PublisherNames()
	if len(names) != 1 || names[0] != "orders_out" {
		t.Fatalf("PublisherNames: got %v want [orders_out]", names)
	}
}

type smokeOrder struct {
	ID string `json:"id"`
}

func TestService_RequestIDPropagatesToDownstreamHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Downstream stub captures the X-Request-ID it receives.
	var gotID string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get("X-Request-ID")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	cfg := Config{}
	cfg.Service.LogLevel = "error"

	svc, err := New[smokeAppCtx, smokeClaims](ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)

	svc.SetContextBuilder(func(c *fiber.Ctx) (smokeAppCtx, error) {
		return smokeAppCtx{}, nil
	})

	// Register a handler that calls the downstream via svc.HTTPC.
	fibermap.RegisterHandler(svc.Engine, "smoke.fanout",
		func(c *fibermap.Context[smokeAppCtx]) error {
			req, _ := http.NewRequestWithContext(c.Ctx.UserContext(), "GET", upstream.URL, nil)
			resp, err := svc.HTTPC.Do(req)
			if err != nil {
				return err
			}
			_ = resp.Body.Close()
			return c.SendStatus(http.StatusOK)
		})
	if err := svc.Engine.LoadBytes([]byte(`
groups:
  - prefix: /
    routes:
      - {method: GET, path: /fanout, handler: smoke.fanout, name: smoke.fanout}
`)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(svc.Logger())})
	// fibermap.RequestID middleware must run so id is set in UserContext.
	app.Use(fibermap.RequestID())
	if err := svc.Engine.Mount(app); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	req := httptest.NewRequest("GET", "/fanout", nil)
	req.Header.Set("X-Request-ID", "smoke-rid")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if gotID != "smoke-rid" {
		t.Fatalf("downstream X-Request-ID = %q, want %q (propagation broken)", gotID, "smoke-rid")
	}
}
