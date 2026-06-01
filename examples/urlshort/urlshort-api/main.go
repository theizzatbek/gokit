// Command urlshort-api is the HTTP frontend of the urlshort sample.
//
// Responsibilities (this binary):
//
//   - Authn / authz: /auth/register, /auth/login, JWT issue
//   - CRUD on the links table: create, list, update, delete, redirect
//   - Schema ownership: applies shared/migrations/*.sql at boot
//   - Publishes link lifecycle events via NATS:
//   - urlshort.link.created — through the transactional outbox
//     (writes INSIDE the same db.Tx as the link insert; the kit's
//     outbox worker drains and publishes asynchronously)
//   - urlshort.link.visited — direct fire-and-forget on each
//     redirect; bounded loss on crash is acceptable since clicks
//     are analytics-only
//
// NOT in scope (other services own these):
//
//   - Visit aggregation → urlshort-counter (batched NATS subscriber)
//   - Metadata enrichment → urlshort-enricher (apimap + NATS)
//
// Run:
//
//	make up && make run-api    # local Postgres + NATS + this service
//	curl -X POST http://localhost:3000/auth/register \
//	  -H 'content-type: application/json' \
//	  -d '{"email":"a@b.com","password":"hunter2hunter2"}'
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-playground/validator/v10"

	"github.com/theizzatbek/gokit/clients/cache"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/db/outbox"
	"github.com/theizzatbek/gokit/service"

	"github.com/theizzatbek/gokit/examples/urlshort/shared/events"
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

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// SSRF guard via the links-package safe_url validator.
	v := validator.New(validator.WithRequiredStructEnabled())
	if err := links.RegisterValidators(v); err != nil {
		return err
	}

	svc, err := service.New[appctx.AppCtx, users.Claims](ctx, cfg.Config,
		service.WithValidator(v),
		service.WithNATSMap(),
		service.WithRoutes(),
		service.WithOpenAPI(),
		// 64 KiB is plenty for the largest payload urlshort accepts.
		service.WithBodyLimit(64*1024),
		service.WithNATSMapRegistration(func(e *natsmap.Engine) {
			// Publishers only — the counter / enricher own their
			// subscribers in their own binaries.
			natsmap.RegisterPublisher[events.LinkCreated](e, events.SubjectLinkCreated)
			natsmap.RegisterPublisher[events.LinkVisited](e, events.SubjectLinkVisited)
		}),
		// api owns the schema — runs migrations on every boot.
		service.WithMigrations(migrations.FS()),
		// Transactional outbox: writes LinkCreated inside the same
		// db.Tx as the link insert, drains via natsmap.PublishRaw.
		service.WithOutbox(
			outbox.WithRetention(7*24*time.Hour),
		),
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
	visitPub := publisher.NewVisit(svc.NATSMap, svc.Logger())
	usersSvc := users.NewService(svc.DB, svc.Hasher)
	linksSvc := links.NewService(svc.DB, visitPub, linkCache)

	svc.SetContextBuilder(appctx.NewContextBuilder(svc.Auth, svc.Logger()))
	users.RegisterHandlers(svc.Engine, usersSvc, svc.Auth)
	links.RegisterHandlers(svc.Engine, linksSvc, cfg.ShortURLBase)

	return svc.Run()
}
