// Command urlshort-publisher is the HTTP→NATS gateway + outbox
// drainer of urlshort.
//
// Responsibilities (this binary):
//
//   - POST /publish/:subject — accepts a raw payload and republishes
//     it onto the matching NATS subject. Backed by the kit's
//     natsmap/natsgw package — the gateway logic lives in the kit,
//     this binary just opts in via service.WithNATSMapGateway.
//   - Drains the outbox table (shared with urlshort-api through the
//     same Postgres database) and publishes each row through the
//     same natsmap engine. The kit's outbox worker polls at sub-second
//     cadence so backlog latency stays low after a fresh api commit.
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
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/clients/natsmap/natsgw"
	"github.com/theizzatbek/gokit/db/outbox"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/service"

	"github.com/theizzatbek/gokit/examples/urlshort/shared/events"
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

func main() { service.Boot(run) }

func run(ctx context.Context) error {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation,
			"urlshort_publisher_env_parse_failed",
			"urlshort-publisher: env parse failed")
	}

	svc, err := service.New[AppCtx, Claims](ctx, cfg.Config,
		service.WithNATSMap(),
		service.WithNATSMapRegistration(func(e *natsmap.Engine) {
			natsmap.RegisterPublisher[events.LinkCreated](e, events.SubjectLinkCreated)
			natsmap.RegisterPublisher[events.LinkVisited](e, events.SubjectLinkVisited)
		}),
		// Kit-managed HTTP→NATS gateway. Allowlist the two urlshort
		// subjects so the public POST /publish surface can't be used
		// to broadcast on arbitrary internal-bus subjects.
		//
		// Each subject also gets a typed validator: the gateway
		// rejects malformed payloads at the HTTP edge with a 400 +
		// natsgw_validation_failed Code so subscribers downstream
		// never see undecodable rows.
		service.WithNATSMapGateway("/publish",
			natsgw.WithSubjectAllowlist(
				events.SubjectLinkCreated,
				events.SubjectLinkVisited,
			),
			natsgw.WithSubjectValidator(events.SubjectLinkCreated,
				natsgw.UnmarshalAs[events.LinkCreated]()),
			natsgw.WithSubjectValidator(events.SubjectLinkVisited,
				natsgw.UnmarshalAs[events.LinkVisited]()),
		),
		// Outbox-worker lives HERE — urlshort-api just INSERTs into
		// the outbox table inside its db.Tx, and this worker drains.
		service.WithOutbox(
			outbox.WithRetention(7*24*time.Hour),
		),
		service.WithPreflightEndpoint(""),
	)
	if err != nil {
		return err
	}
	defer svc.Close()

	return svc.Run()
}
