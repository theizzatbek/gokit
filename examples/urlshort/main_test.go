package main

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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"
	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/clients/apimap"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/appctx"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/config"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/enrich"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/events"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/links"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/users"
	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/service"
)

// TestSmoke_EndToEnd exercises every gokit package: fibermap routes,
// errs error handler, db CRUD, auth (register/login/JWT), httpc HTML
// fetch, apimap MicroLink call, natsclient publish. Requires Docker.
func TestSmoke_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("smoke test requires Docker; skip with -short")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	dbCfg := startPostgres(t, ctx)
	natsURL := startNATS(t, ctx)
	upstream := startUpstreamStub(t)
	pemKey := generateEd25519PEM(t)

	cfg := config.Config{
		Config: service.Config{
			DB:   dbCfg,
			Auth: service.AuthConfig{PrivateKeyPEM: pemKey, KID: "k1", Issuer: "urlshort", AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour},
			NATS: service.NATSConfig{URL: natsURL},
		},
		MicrolinkBaseURL: upstream.URL,
		ShortURLBase:     "http://test.local",
	}
	cfg.Service.LogLevel = "error"

	v := validator.New(validator.WithRequiredStructEnabled())
	if err := links.RegisterValidators(v); err != nil {
		t.Fatalf("links.RegisterValidators: %v", err)
	}

	svc, err := service.New[appctx.AppCtx, users.Claims](ctx, cfg.Config,
		service.WithValidator(v),
		service.WithAPIMap(),
		service.WithNATSMap(),
		service.WithRoutes(),
		service.WithAPIMapEnv(map[string]string{
			"MICROLINK_BASE_URL": upstream.URL,
		}),
		service.WithAPIMapRegistration(func(e *apimap.Engine) {
			apimap.RegisterResponse[enrich.MicroLinkResp](e, "microlink.metadata")
			apimap.RegisterResponse[[]byte](e, "web.fetch")
		}),
		service.WithNATSMapRegistration(func(e *natsmap.Engine) {
			natsmap.RegisterPublisher[events.LinkCreated](e, "urlshort.link.created")
			natsmap.RegisterPublisher[events.LinkVisited](e, "urlshort.link.visited")
		}),
	)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	// Apply migrations + ensure stream — same as production main.go.
	sqlBytes, err := os.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DB.Exec(ctx, string(sqlBytes)); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := svc.NATS.EnsureStream(ctx, natsclient.StreamConfig{
		Name: "URLSHORT", Subjects: []string{"urlshort.>"}, MaxAge: 24 * time.Hour, Storage: natsclient.StorageFile,
	}); err != nil {
		t.Fatalf("ensure stream: %v", err)
	}

	fetcher := enrich.NewFetcher(svc.APIMap, svc.Logger())
	usersSvc := users.NewService(svc.DB, svc.Hasher)
	pub := events.NewPublisher(svc.NATSMap, svc.Logger())
	linksSvc := links.NewService(svc.DB, fetcher.FetchMetadata, pub)
	svc.SetContextBuilder(appctx.NewContextBuilder(svc.Auth, svc.Logger()))
	users.RegisterHandlers(svc.Engine, usersSvc, svc.Auth)
	links.RegisterHandlers(svc.Engine, linksSvc, cfg.ShortURLBase)

	// Load routes.yaml explicitly — svc.Run() auto-loads when Routes.Enabled,
	// but this test exercises app.Test (no Run), so we load here.
	if err := svc.Engine.LoadFile("routes.yaml"); err != nil {
		t.Fatalf("LoadFile routes.yaml: %v", err)
	}

	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(svc.Logger())})
	// Same Bearer-optional layer that service.Run installs via WithUse.
	app.Use(svc.Auth.Bearer(auth.BearerOptional))
	if err := svc.Engine.Mount(app); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	// Subscribe to NATS events.
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(nc.Close)
	events := make(chan *nats.Msg, 32)
	if _, err := nc.Subscribe("urlshort.>", func(m *nats.Msg) { events <- m }); err != nil {
		t.Fatalf("nats subscribe: %v", err)
	}

	// 1. Register
	regResp := doJSON(t, app, "POST", "/auth/register",
		`{"email":"a@b.com","password":"hunter2hunter2"}`, "")
	requireStatus(t, regResp, fiber.StatusCreated)

	// 2. Login → grab JWT from the access cookie (or response body — depends
	// on auth.LoginHandler shape). Inspect the response to find the access token.
	loginResp := doJSON(t, app, "POST", "/auth/login",
		`{"login":"a@b.com","password":"hunter2hunter2"}`, "")
	requireStatus(t, loginResp, fiber.StatusOK)
	token := extractAccessToken(t, loginResp)
	if token == "" {
		t.Fatal("login: no access token in response")
	}

	// 3. Shorten
	target := upstream.URL + "/the-target"
	shortenResp := doJSON(t, app, "POST", "/links",
		`{"url":"`+target+`"}`, token)
	requireStatus(t, shortenResp, fiber.StatusCreated)
	var shortenBody map[string]any
	if err := json.NewDecoder(shortenResp.Body).Decode(&shortenBody); err != nil {
		t.Fatalf("decode shorten body: %v", err)
	}
	code, _ := shortenBody["code"].(string)
	if code == "" {
		t.Fatalf("shorten: missing code in %v", shortenBody)
	}
	title, _ := shortenBody["title"].(string)
	if title != "The Real Title" {
		t.Errorf("title = %q, want %q (httpc HTML parse)", title, "The Real Title")
	}

	// 4. NATS event arrived
	waitForSubject(t, events, "urlshort.link.created", 5*time.Second)

	// 5. Redirect (public)
	redirResp := doJSON(t, app, "GET", "/"+code, "", "")
	if redirResp.StatusCode != fiber.StatusFound {
		t.Errorf("redirect status = %d, want 302", redirResp.StatusCode)
	}
	if loc := redirResp.Header.Get("Location"); loc != target {
		t.Errorf("Location = %q, want %q", loc, target)
	}

	// 6. Stats
	statsResp := doJSON(t, app, "GET", "/links/"+code+"/stats", "", token)
	requireStatus(t, statsResp, fiber.StatusOK)
	var stats map[string]any
	if err := json.NewDecoder(statsResp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if vc, _ := stats["visit_count"].(float64); vc != 1 {
		t.Errorf("visit_count = %v, want 1", stats["visit_count"])
	}

	// 7. NATS visited event
	waitForSubject(t, events, "urlshort.link.visited", 5*time.Second)
}

// --- container + stub setup ---

func startPostgres(t *testing.T, ctx context.Context) db.Config {
	t.Helper()
	if testing.Short() {
		t.Skip("requires Postgres testcontainer; rerun without -short")
	}
	c, err := tcpg.Run(ctx, "postgres:16-alpine",
		tcpg.WithDatabase("urlshort"),
		tcpg.WithUsername("urlshort"),
		tcpg.WithPassword("urlshort"),
		tcpg.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("postgres testcontainer: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port.Port())
	return db.Config{
		Host:     host,
		Port:     p,
		User:     "urlshort",
		Password: "urlshort",
		Database: "urlshort",
		SSLMode:  "disable",
	}
}

func startNATS(t *testing.T, ctx context.Context) string {
	t.Helper()
	c, err := tcnats.Run(ctx, "nats:2-alpine", testcontainers.WithCmd("-js"))
	if err != nil {
		t.Fatalf("nats testcontainer: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return url
}

func startUpstreamStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/the-target":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<html><head><title>The Real Title</title></head></html>`)
		case "/":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"success","data":{"title":"From MicroLink","description":"desc","image":{"url":"http://x/img.png"}}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// generateEd25519PEM creates a fresh Ed25519 private key in PEM form.
func generateEd25519PEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return string(pemBytes)
}

// --- HTTP request helpers ---

func doJSON(t *testing.T, app *fiber.App, method, path, body, bearer string) *http.Response {
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
		t.Fatalf("app.Test %s %s: %v", method, path, err)
	}
	return resp
}

func requireStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d, body = %s", resp.StatusCode, want, b)
	}
}

// extractAccessToken inspects the login response and pulls out the access
// JWT. auth.LoginHandler returns {"access":"<jwt>","expires_in":...} per
// gokit/auth/handlers.go, so we just decode that.
func extractAccessToken(t *testing.T, resp *http.Response) string {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	s, _ := body["access_token"].(string)
	return s
}

func waitForSubject(t *testing.T, ch <-chan *nats.Msg, subject string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case m := <-ch:
			if m.Subject == subject {
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting for NATS subject %q", subject)
		}
	}
}
