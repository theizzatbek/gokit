package service

import (
	"context"

	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/db/outbox"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// buildOutbox constructs the outbox.Worker when WithOutbox was
// passed AND its dependencies (DB + a publisher target) are wired.
// No-op when the option wasn't passed — the kit stays zero-cost for
// services that don't need an outbox.
//
// Validation:
//   - WithOutbox without Config.DB → service_outbox_needs_db.
//   - WithOutbox without NATSMap AND without WithOutboxDispatcher →
//     service_outbox_needs_natsmap (the default publish path has
//     nowhere to dispatch to).
//
// The constructed worker auto-wires the kit logger + metrics
// registry so outbox_* series land alongside the rest of the kit
// on the same /metrics endpoint. OnShutdown(Stop) is registered so
// Service.Close drains gracefully before the DB pool closes.
func (s *Service[T, C]) buildOutbox(ctx context.Context) error {
	if !s.opts.outboxEnable {
		return nil
	}
	if s.DB == nil {
		return xerrs.Validation(CodeOutboxNeedsDB,
			"service: WithOutbox requires DB to be configured")
	}
	dispatch := s.opts.outboxDispatch
	if dispatch == nil {
		if s.NATSMap == nil {
			return xerrs.Validation(CodeOutboxNeedsNATSMap,
				"service: WithOutbox needs NATSMap (or pass WithOutboxDispatcher)")
		}
		rt := s.NATSMap
		dispatch = func(ctx context.Context, e outbox.Event) error {
			return natsmap.PublishRaw(ctx, rt, e.EventType, e.Payload, e.Headers)
		}
	}
	if s.opts.outboxAutoSchema {
		if _, err := s.DB.Exec(ctx, outbox.Schema()); err != nil {
			return xerrs.Wrap(err, xerrs.KindInternal, CodeOutboxSchemaFailed,
				"service: outbox schema apply failed")
		}
	}
	defaults := []outbox.WorkerOption{
		outbox.WithLogger(s.logger),
		outbox.WithMetrics(s.metrics),
	}
	all := append(defaults, s.opts.outboxOpts...)
	w, err := outbox.NewWorker(s.DB, dispatch, all...)
	if err != nil {
		return err
	}
	if err := w.Start(ctx); err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeOutboxStartFailed,
			"service: outbox start failed")
	}
	s.OnShutdown(w.Stop)
	s.Outbox = w
	return nil
}
