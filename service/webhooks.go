package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/clients/webhooks"
)

// WebhooksConfig is the input to [WithWebhooks]. SubStore + DeliveryStore
// are typically built via clients/webhooks/storepg.
//
// Worker drains pending deliveries with per-target backoff/DLQ;
// Fanout maps one inbound Event to N deliveries (one per matching
// subscription). The kit registers Worker.Stop with [Service.OnShutdown]
// so in-flight HTTP calls drain before the NATS connection closes.
//
// The kit does NOT auto-register Fanout as a NATS subscriber — the
// fan-out wiring depends on which event types the application
// publishes. Use `s.WebhooksFanout.HandleEvent` from your own
// natsmap handler (registered via [WithNATSMapRegistration]) or
// from a [db/notify] listener.
type WebhooksConfig struct {
	SubStore      webhooks.SubscriptionStore
	DeliveryStore webhooks.DeliveryStore
	StartWorker   bool          // when true, kit starts a DeliveryWorker
	StartFanout   bool          // when true, kit exposes Fanout on Service.WebhooksFanout
	HTTPClient    *http.Client  // optional; defaults to Service.HTTPC
	WorkerOptions WorkerOptions // optional tuning for Worker
}

// WorkerOptions surfaces a subset of webhooks.WorkerConfig — the
// fields users typically tune at service-construction time.
// Anything left zero falls through to webhooks defaults
// (MaxAttempts=8, BatchSize=32, Interval=1s, MaxInFlight=16).
type WorkerOptions struct {
	MaxAttempts int
	BatchSize   int
	Interval    int // seconds; 0 → default 1s
	MaxInFlight int
}

// WithWebhooks wires the webhooks subsystem into Service. The
// SubStore/DeliveryStore must be built by the caller (typically via
// clients/webhooks/storepg.NewSubStore / NewDeliveryStore) so the
// kit stays neutral to storage choice.
//
//	subStore, _ := storepg.NewSubStore(svc.DB, secretKey)
//	delivStore, _ := storepg.NewDeliveryStore(svc.DB, secretKey)
//	service.New[T, C](ctx, cfg,
//	    service.WithDB(...),
//	    service.WithWebhooks(service.WebhooksConfig{
//	        SubStore:      subStore,
//	        DeliveryStore: delivStore,
//	        StartWorker:   true,
//	        StartFanout:   true,
//	    }),
//	)
//
// `Service.WebhooksWorker` / `Service.WebhooksFanout` are exposed
// for callers that need direct access (e.g. registering Fanout as
// a natsmap handler).
func WithWebhooks(cfg WebhooksConfig) Option {
	return func(o *options) { o.webhooksCfg = &cfg }
}

func (s *Service[T, C]) buildWebhooks(ctx context.Context) error {
	cfg := s.opts.webhooksCfg
	if cfg == nil {
		return nil
	}
	if cfg.SubStore == nil || cfg.DeliveryStore == nil {
		return errors.New("service: WithWebhooks requires both SubStore and DeliveryStore")
	}

	if cfg.StartFanout {
		fanout, err := webhooks.NewFanout(webhooks.FanoutConfig{
			SubStore:      cfg.SubStore,
			DeliveryStore: cfg.DeliveryStore,
			DB:            s.DB,
		})
		if err != nil {
			return err
		}
		s.WebhooksFanout = fanout
	}

	if cfg.StartWorker {
		httpClient := cfg.HTTPClient
		if httpClient == nil {
			httpClient = s.HTTPC
		}
		var metricsReg prometheus.Registerer
		if s.metrics != nil {
			metricsReg = s.metrics
		}
		var logger *slog.Logger
		if s.logger != nil {
			logger = s.logger.With("subsystem", "webhooks")
		}
		w, err := webhooks.NewWorker(webhooks.WorkerConfig{
			SubStore:      cfg.SubStore,
			DeliveryStore: cfg.DeliveryStore,
			HTTPClient:    httpClient,
			MaxAttempts:   cfg.WorkerOptions.MaxAttempts,
			BatchSize:     cfg.WorkerOptions.BatchSize,
			MaxInFlight:   cfg.WorkerOptions.MaxInFlight,
			Logger:        logger,
			Metrics:       metricsReg,
		})
		if err != nil {
			return err
		}
		s.WebhooksWorker = w
		w.Start(ctx)
		// Register the drain so Worker stops BEFORE the kit closes
		// the NATS / DB resources it depends on (OnShutdown is LIFO).
		s.OnShutdown(func() error {
			return w.Stop(context.Background())
		})
	}
	return nil
}
