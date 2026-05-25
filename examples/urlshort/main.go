// Command urlshort is the gokit integration example — a URL-shortener
// service that uses every kit package in its natural role.
//
// Run:
//
//	make up && make run    # local Postgres + NATS + service
//	curl -X POST http://localhost:3000/auth/register -H 'content-type: application/json' \
//	  -d '{"email":"a@b.com","password":"hunter2hunter2"}'
//	# … login → shorten → redirect → stats. See README.md for the full walkthrough.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/fibermount"
	"github.com/theizzatbek/gokit/auth/refreshpg"
	"github.com/theizzatbek/gokit/clients/apimap"
	"github.com/theizzatbek/gokit/clients/httpc"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/appctx"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/config"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/enrich"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/events"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/links"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/users"
	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/fibermap/openapi"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel)

	deps, err := buildDeps(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer deps.Close()

	eng, err := buildEngine(ctx, cfg, logger, deps)
	if err != nil {
		return err
	}
	return eng.Run(runOptions(cfg, logger)...)
}

// runOptions returns the production-ops bundle.
func runOptions(cfg config.Config, logger *slog.Logger) []fibermap.RunOption {
	return []fibermap.RunOption{
		fibermap.WithAddr(cfg.Addr),
		fibermap.WithRequestLogger(logger),
		fibermap.WithMetrics("/metrics"),
		fibermap.WithHealthCheck("/healthz"),
		fibermap.WithRecover(logger),
	}
}

// deps owns every long-lived resource. Close() releases them in
// reverse order. *http.Client (from httpc) needs no close — it sits
// inside enrich.Fetcher and is GC'd when deps drops.
type deps struct {
	db       *db.DB
	authObj  *auth.Auth[users.Claims]
	hasher   *auth.Hasher
	apiCli   *apimap.Client
	natsCli  *natsclient.Client
	pub      *events.Publishers
	fetcher  *enrich.Fetcher
	usersSvc *users.Service
	linksSvc *links.Service
}

func buildDeps(ctx context.Context, cfg config.Config, logger *slog.Logger) (*deps, error) {
	// --- Postgres
	d, err := db.Connect(ctx, cfg.DB, db.WithLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}

	if err := applyMigrations(ctx, d, "migrations/0001_init.sql"); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// --- Auth (JWT + refresh persisted in Postgres)
	keySet, err := auth.LoadKeysFromPEM("k1", map[string][]byte{
		"k1": []byte(cfg.JWTPrivateKeyPEM),
	})
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("auth keys: %w", err)
	}
	authObj, err := auth.New[users.Claims](auth.Config{
		Issuer:     "urlshort",
		Keys:       keySet,
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 30 * 24 * time.Hour,
	}, auth.WithRefreshStore(refreshpg.New(d)))
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("auth new: %w", err)
	}
	hasher := auth.NewHasher(auth.DefaultParams())

	// --- Outbound HTTP client (used by enrich.Fetcher for arbitrary URL fetch)
	httpClient, err := httpc.New(httpc.Config{
		Timeout:    5 * time.Second,
		MaxRetries: 2,
	}, httpc.WithLogger(logger))
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("httpc new: %w", err)
	}

	// --- apimap: MicroLink declarative client
	apiEng := apimap.New()
	if err := apiEng.LoadFile("clients.yaml"); err != nil {
		d.Close()
		return nil, fmt.Errorf("apimap load: %w", err)
	}
	apimap.RegisterResponse[enrich.MicroLinkResp](apiEng, "microlink.metadata")
	apiClient, err := apiEng.Build(apimap.WithLogger(logger))
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("apimap build: %w", err)
	}

	fetcher := enrich.NewFetcher(httpClient, apiClient, logger)

	// --- NATS JetStream
	nc, err := natsclient.Connect(ctx, natsclient.Config{URL: cfg.NATSURL}, natsclient.WithLogger(logger))
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	if err := nc.EnsureStream(ctx, natsclient.StreamConfig{
		Name:     "URLSHORT",
		Subjects: []string{"urlshort.>"},
		MaxAge:   7 * 24 * time.Hour,
		Storage:  natsclient.StorageFile,
	}); err != nil {
		nc.Close()
		d.Close()
		return nil, fmt.Errorf("nats ensure stream: %w", err)
	}
	pub := events.New(nc, logger)

	// --- Services
	usersSvc := users.NewService(d, hasher)
	linksSvc := links.NewService(d,
		fetcher.FetchMetadata,
		func(ctx context.Context, l links.Link) {
			pub.PublishCreated(ctx, events.LinkCreated{
				LinkID: l.ID, UserID: l.UserID, Code: l.Code,
				URL: l.OriginalURL, Title: l.Title, CreatedAt: l.CreatedAt,
			})
		},
		func(ctx context.Context, code, ua, ip string) {
			pub.PublishVisited(ctx, events.LinkVisited{
				Code: code, VisitedAt: time.Now(), UserAgent: ua, IP: ip,
			})
		},
	)

	return &deps{
		db:       d,
		authObj:  authObj,
		hasher:   hasher,
		apiCli:   apiClient,
		natsCli:  nc,
		pub:      pub,
		fetcher:  fetcher,
		usersSvc: usersSvc,
		linksSvc: linksSvc,
	}, nil
}

func (d *deps) Close() {
	if d == nil {
		return
	}
	if d.natsCli != nil {
		d.natsCli.Close()
	}
	if d.db != nil {
		d.db.Close()
	}
}

// buildEngine wires the engine, registers handlers, mounts auth login/refresh/
// logout as programmatic routes, and sets up OpenAPI generation. It does NOT
// call Run — the caller decides whether to Run() or Mount() to an existing
// *fiber.App (the smoke test takes the latter path).
func buildEngine(ctx context.Context, cfg config.Config, logger *slog.Logger, d *deps) (*fibermap.Engine[appctx.AppCtx], error) {
	_ = ctx
	eng := fibermap.Default[appctx.AppCtx]()
	eng.SetContextBuilder(appctx.NewContextBuilder(d.authObj, logger))
	eng.SetValidator(validator.New(validator.WithRequiredStructEnabled()))

	// Wire the bearer / require_scope / require_role middleware
	// factories. Used by routes.yaml.
	if err := fibermount.MountMiddlewareFactories(eng, d.authObj); err != nil {
		return nil, fmt.Errorf("fibermount: %w", err)
	}

	// Register fibermap handlers (typed AppCtx).
	users.RegisterHandlers(eng, d.usersSvc, d.authObj)
	links.RegisterHandlers(eng, d.linksSvc, cfg.ShortURLBase)

	// Mount auth handlers (raw *fiber.Ctx) as programmatic fibermap
	// routes by wrapping them in a typed adapter. These ARE part of
	// the engine, so they participate in OpenAPI generation and the
	// engine-wide ContextBuilder/middleware chain.
	wrap := func(h fiber.Handler) fibermap.HandlerFunc[appctx.AppCtx] {
		return func(c *fibermap.Context[appctx.AppCtx]) error { return h(c.Ctx) }
	}
	eng.Add("POST", "/auth/login", "auth.login", wrap(d.authObj.LoginHandler))
	eng.Add("POST", "/auth/refresh", "auth.refresh", wrap(d.authObj.RefreshHandler))
	eng.Add("POST", "/auth/logout", "auth.logout", wrap(d.authObj.LogoutHandler))

	if err := eng.LoadFile("routes.yaml"); err != nil {
		return nil, fmt.Errorf("load routes: %w", err)
	}

	// OpenAPI generator — mounts /openapi.json + /docs (Scalar UI).
	gen := openapi.NewGenerator(eng,
		openapi.WithInfo(openapi.Info{
			Title:       "urlshort API",
			Version:     "0.1.0",
			Description: "gokit integration example — URL shortener.",
		}),
	)
	if err := gen.Mount(); err != nil {
		return nil, fmt.Errorf("openapi mount: %w", err)
	}

	return eng, nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

func applyMigrations(ctx context.Context, d *db.DB, path string) error {
	sqlBytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = d.Exec(ctx, string(sqlBytes))
	return err
}
