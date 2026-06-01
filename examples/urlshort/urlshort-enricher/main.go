// Command urlshort-enricher is the metadata-fetcher worker.
//
// Responsibilities (this binary):
//
//   - Subscribe to urlshort.link.created via natsmap (one-by-one,
//     not batched — see configs/subscribers.yaml).
//   - For each event: call Microlink (description + image_url) +
//     open-client HTML fetch (title), best-effort.
//   - UPDATE the matching link row in Postgres with whatever
//     metadata was retrieved.
//
// NOT in scope:
//
//   - HTTP / handlers (kit's auto-mounted /healthz, /readyz, /metrics).
//   - Migrations — urlshort-api owns the schema.
//   - LinkVisited handling — that's urlshort-counter.
//
// Run:
//
//	make run-enricher    # same NATS + Postgres + APIMap (Microlink)
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/caarlos0/env/v11"

	"github.com/theizzatbek/gokit/clients/apimap"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/service"

	"github.com/theizzatbek/gokit/examples/urlshort/shared/events"
	"github.com/theizzatbek/gokit/examples/urlshort/urlshort-enricher/internal/enrich"
	"github.com/theizzatbek/gokit/examples/urlshort/urlshort-enricher/internal/enricher"
)

// Config embeds service.Config plus the Microlink base URL —
// enricher-specific knob.
type Config struct {
	service.Config

	// MicrolinkBaseURL is substituted into clients.yaml's
	// ${MICROLINK_BASE_URL} env reference by the apimap loader.
	MicrolinkBaseURL string `env:"MICROLINK_BASE_URL" envDefault:"https://api.microlink.io"`
}

// AppCtx + Claims are placeholders — no inbound HTTP traffic, no auth.
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
			"urlshort_enricher_env_parse_failed",
			"urlshort-enricher: env parse failed")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var en *enricher.Enricher

	svc, err := service.New[AppCtx, Claims](ctx, cfg.Config,
		service.WithAPIMap(),
		service.WithNATSMap(),
		service.WithAPIMapEnv(map[string]string{
			"MICROLINK_BASE_URL": cfg.MicrolinkBaseURL,
		}),
		service.WithAPIMapRegistration(func(e *apimap.Engine) {
			apimap.RegisterResponse[enrich.MicroLinkResp](e, "microlink.metadata")
			apimap.RegisterResponse[[]byte](e, "web.fetch")
		}),
		service.WithNATSMapRegistration(func(e *natsmap.Engine) {
			natsmap.RegisterHandler[events.LinkCreated](e, "link_enricher",
				func(ctx context.Context, msg natsclient.Msg[events.LinkCreated]) error {
					return en.Handle(ctx, msg)
				})
		}),
		service.WithPreflightEndpoint(""),
	)
	if err != nil {
		return err
	}
	defer svc.Close()

	fetcher := enrich.NewFetcher(svc.APIMap, svc.Logger())
	en = enricher.New(svc.DB, fetcher, svc.Logger())
	return svc.Run()
}
