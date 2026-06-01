// Command urlshort-publisher is the HTTP→NATS gateway + outbox
// drainer of urlshort.
//
// Responsibilities (this binary):
//
//   - POST /publish — accepts {subject, payload, headers?} JSON and
//     republishes the payload bytes onto the matching NATS subject
//     via natsmap.PublishRaw. Used by urlshort-api for the
//     fire-and-forget LinkVisited path.
//   - Drains the outbox table (shared with urlshort-api through the
//     same Postgres database) and publishes each row through the
//     same natsmap engine. The kit's outbox worker handles
//     LISTEN/NOTIFY so backlog latency is sub-second after a fresh
//     api commit.
//
// NOT in scope:
//
//   - Schema ownership — urlshort-api applies the migrations
//     (including the outbox table DDL). The publisher trusts the
//     schema is present.
//   - Subscribers — urlshort-counter / urlshort-enricher consume
//     events directly from NATS.
//
// Run:
//
//	make run-publisher    # boots against the same Postgres + NATS as api
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/db/outbox"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/service"

	"github.com/theizzatbek/gokit/examples/urlshort/shared/events"
	"github.com/theizzatbek/gokit/examples/urlshort/urlshort-publisher/internal/gateway"
)

// Config embeds service.Config — no publisher-specific knobs today.
type Config struct {
	service.Config
}

// AppCtx + Claims placeholders — gateway routes are unauthenticated
// (intra-network only), no per-request payload to carry.
type (
	AppCtx struct{}
	Claims struct{}
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run() error {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation,
			"urlshort_publisher_env_parse_failed",
			"urlshort-publisher: env parse failed")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	svc, err := service.New[AppCtx, Claims](ctx, cfg.Config,
		service.WithNATSMap(),
		service.WithRoutes(),
		service.WithOpenAPI(),
		service.WithNATSMapRegistration(func(e *natsmap.Engine) {
			natsmap.RegisterPublisher[events.LinkCreated](e, events.SubjectLinkCreated)
			natsmap.RegisterPublisher[events.LinkVisited](e, events.SubjectLinkVisited)
		}),
		// Outbox-worker lives HERE — urlshort-api just INSERTs into
		// the outbox table inside its db.Tx, and this worker drains.
		// 7-day retention gives operators a window to inspect /
		// replay published rows.
		service.WithOutbox(
			outbox.WithRetention(7*24*time.Hour),
		),
		service.WithPreflightEndpoint(""),
	)
	if err != nil {
		return err
	}
	defer svc.Close()

	fibermap.RegisterHandler(svc.Engine, "gateway.publish",
		gateway.Handler[AppCtx](svc.NATSMap))

	return svc.Run()
}
