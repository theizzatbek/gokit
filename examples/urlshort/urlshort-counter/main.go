// Command urlshort-counter is the batched visit-counter worker.
//
// Responsibilities (this binary):
//
//   - Subscribe to urlshort.link.visited via natsmap's pull-batched
//     subscriber (batch_size=1000, batch_interval=1s — see
//     configs/subscribers.yaml).
//   - Aggregate the per-batch events in memory by code, then run ONE
//     UPDATE … FROM unnest(...) against Postgres so a hot link is
//     ONE row-update regardless of how many clicks fell into the
//     batch.
//   - All-or-nothing ack: on UPDATE success natsmap Acks the whole
//     batch; on UPDATE failure natsmap Naks all → JetStream redelivers.
//
// NOT in scope:
//
//   - HTTP / handlers — the kit's auto-mounted /healthz, /readyz,
//     /metrics are enough for K8s.
//   - Migrations — urlshort-api owns the schema. Counter assumes the
//     links table is present + idempotent on startup.
//
// Run:
//
//	make run-counter    # boots against the same NATS + Postgres as api
package main

import (
	"context"

	"github.com/caarlos0/env/v11"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/service"

	"github.com/theizzatbek/gokit/examples/urlshort/shared/events"
	"github.com/theizzatbek/gokit/examples/urlshort/urlshort-counter/internal/counter"
)

// Config embeds service.Config — no counter-specific knobs today.
type Config struct {
	service.Config
}

// AppCtx is the per-request payload type. The counter has no inbound
// HTTP traffic so the type is empty; kit still requires SOMETHING
// for the generic.
type AppCtx struct{}

// Claims mirrors AppCtx — auth never runs in this binary.
type Claims struct{}

func main() { service.Boot(run) }

func run(ctx context.Context) error {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation,
			"urlshort_counter_env_parse_failed",
			"urlshort-counter: env parse failed")
	}

	// vc is referenced from the natsmap registration callback (runs
	// inside service.New AFTER buildDB has populated svc.DB but
	// BEFORE subscriptions open). Captured by pointer so the
	// handler closure sees the fully-built instance.
	var vc *counter.Counter

	svc, err := service.New[AppCtx, Claims](ctx, cfg.Config,
		service.WithNATSMap(),
		service.WithNATSMapRegistration(func(e *natsmap.Engine) {
			natsmap.RegisterBatchedHandler[events.LinkVisited](e, "link_visit_counter",
				func(ctx context.Context, batch []natsclient.Msg[events.LinkVisited]) error {
					return vc.Handle(ctx, batch)
				})
		}),
		// Wire a preflight endpoint so `kit doctor` (or CI smoke tests)
		// can verify both Postgres and NATS are reachable.
		service.WithPreflightEndpoint(""),
	)
	if err != nil {
		return err
	}
	defer svc.Close()

	vc = counter.New(svc.DB, svc.Logger())
	return svc.Run()
}
