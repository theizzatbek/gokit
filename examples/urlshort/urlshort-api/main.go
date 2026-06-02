// Command urlshort-api is the HTTP frontend of the urlshort sample.
//
// Responsibilities (this binary):
//
//   - Authn / authz: /auth/register, /auth/login, JWT issue
//   - CRUD on the links table: create, list, update, delete, redirect
//   - Schema ownership: applies shared/migrations/*.sql at boot
//   - Writes events:
//   - urlshort.link.created — through the transactional outbox
//     (writes INSIDE the same db.Tx as the link insert; the
//     publisher service drains the outbox table asynchronously)
//   - urlshort.link.visited — fire-and-forget HTTP POST to the
//     publisher gateway; the publisher republishes onto NATS.
//     Bounded loss on crash is acceptable since clicks are
//     analytics-only.
//
// Notably: api does NOT import natsmap. Every NATS-bound event flows
// through urlshort-publisher (either via the shared outbox table or
// via POST /publish). Lets the api binary live in a network zone
// without NATS reachability.
//
// NOT in scope (other services own these):
//
//   - Outbox drain → urlshort-publisher (also hosts POST /publish)
//   - Visit aggregation → urlshort-counter
//   - Metadata enrichment → urlshort-enricher
//
// Run:
//
//	make up && make run-api    # local Postgres + this service
//	curl -X POST http://localhost:3000/auth/register \
//	  -H 'content-type: application/json' \
//	  -d '{"email":"a@b.com","password":"hunter2hunter2"}'
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"

	"github.com/theizzatbek/gokit/clients/cache"
	"github.com/theizzatbek/gokit/clients/httpc"
	"github.com/theizzatbek/gokit/service"

	"github.com/theizzatbek/gokit/examples/urlshort/shared/migrations"
	"github.com/theizzatbek/gokit/examples/urlshort/urlshort-api/internal/appctx"
	"github.com/theizzatbek/gokit/examples/urlshort/urlshort-api/internal/config"
	"github.com/theizzatbek/gokit/examples/urlshort/urlshort-api/internal/links"
	"github.com/theizzatbek/gokit/examples/urlshort/urlshort-api/internal/publisher"
	"github.com/theizzatbek/gokit/examples/urlshort/urlshort-api/internal/users"
)

// linksStatsJob returns a JobFn that logs aggregate link / visit
// stats — same singleton-cron pattern as before the split. The api
// owns this because it owns the migrations / schema.
func linksStatsJob(svc *service.Service[appctx.AppCtx, users.Claims]) service.JobFn {
	return func(ctx context.Context) error {
		var (
			linkCount  int
			totalViews int64
		)
		row := svc.DB.QueryRow(ctx,
			`SELECT count(*), COALESCE(SUM(visit_count), 0) FROM links`)
		if err := row.Scan(&linkCount, &totalViews); err != nil {
			return err
		}
		svc.Logger().Info("daily-stats",
			"links", linkCount, "total_views", totalViews)
		return nil
	}
}

func main() { service.Boot(run) }

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// SSRF guard via the links-package safe_url validator.
	v := validator.New(validator.WithRequiredStructEnabled())
	if err := links.RegisterValidators(v); err != nil {
		return err
	}

	svc, err := service.New[appctx.AppCtx, users.Claims](ctx, cfg.Config,
		service.WithValidator(v),
		service.WithRoutes(),
		service.WithOpenAPI(),
		// 64 KiB is plenty for the largest payload urlshort accepts.
		service.WithBodyLimit(64*1024),
		// api owns the schema — runs migrations on every boot.
		service.WithMigrations(migrations.FS()),
		// Bootstrap the outbox table DDL so api can INSERT into it
		// inside its tx — the worker that drains the table lives in
		// urlshort-publisher.
		service.WithOutboxAutoSchema(),
	)
	if err != nil {
		return err
	}
	defer svc.Close()

	// Daily housekeeping — singleton cron via pg_try_advisory_lock so
	// the log doesn't multiply across replicas.
	if err := svc.AddSingletonCron("daily-stats", "0 3 * * *", linksStatsJob(svc)); err != nil {
		return err
	}

	linkCache := cache.For[links.CachedLink](svc.Redis, "urlshort:link:")

	// Dedicated kit-grade HTTP client for the publisher call —
	// retries + slow-call metrics ride the kit transport chain.
	publisherHTTP, err := httpc.New(httpc.Config{
		Timeout:     2 * time.Second,
		MaxRetries:  2,
		BackoffBase: 50 * time.Millisecond,
	}, httpc.WithLogger(svc.Logger()))
	if err != nil {
		return err
	}
	visitPub := publisher.NewVisit(cfg.PublisherURL, publisherHTTP, svc.Logger())

	usersSvc := users.NewService(svc.DB, svc.Hasher)
	linksSvc := links.NewService(svc.DB, visitPub, linkCache)

	svc.SetContextBuilder(appctx.NewContextBuilder(svc.Auth, svc.Logger()))
	users.RegisterHandlers(svc.Engine, usersSvc, svc.Auth)
	links.RegisterHandlers(svc.Engine, linksSvc, cfg.ShortURLBase)

	return svc.Run()
}

// guard so http import doesn't become unused if a future refactor
// drops the publisher http client. Keeps the package import-graph
// stable across iterations.
var _ = http.MethodPost
