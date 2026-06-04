package webhooks

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// FanoutConfig wires Fanout dependencies. DB is optional — if set,
// HandleEvent runs Enqueue inside DB.Tx so the delivery INSERTs
// commit atomically with the surrounding work. If nil, Enqueue runs
// directly on a Querier supplied by the caller (rare).
type FanoutConfig struct {
	SubStore      SubscriptionStore
	DeliveryStore DeliveryStore
	DB            *db.DB
}

// Fanout turns one inbound Event into N webhook_deliveries rows.
type Fanout struct {
	cfg FanoutConfig
}

// NewFanout validates dependencies and returns a Fanout.
func NewFanout(cfg FanoutConfig) (*Fanout, error) {
	if cfg.SubStore == nil {
		return nil, errs.Validation("webhooks_fanout_no_sub_store", "FanoutConfig.SubStore required")
	}
	if cfg.DeliveryStore == nil {
		return nil, errs.Validation("webhooks_fanout_no_delivery_store", "FanoutConfig.DeliveryStore required")
	}
	return &Fanout{cfg: cfg}, nil
}

// HandleEvent looks up active subscribers for ev.EventType and
// enqueues one Delivery per matching subscription. Idempotent
// through the UNIQUE (subscription_id, event_id) constraint —
// callers may retry HandleEvent with the same ev.ID safely.
func (f *Fanout) HandleEvent(ctx context.Context, ev Event) error {
	if len(ev.Payload) > MaxPayloadBytes {
		return errs.Validationf(CodePayloadTooLarge,
			"webhooks: payload %d bytes exceeds %d", len(ev.Payload), MaxPayloadBytes)
	}
	subs, err := f.cfg.SubStore.ListByEvent(ctx, ev.EventType)
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		return nil
	}
	now := time.Now()
	deliveries := make([]Delivery, 0, len(subs))
	for _, s := range subs {
		deliveries = append(deliveries, Delivery{
			ID:             uuid.New(),
			SubscriptionID: s.ID,
			EventID:        ev.ID,
			EventType:      ev.EventType,
			Payload:        ev.Payload,
			Headers:        ev.Headers,
			Status:         DeliveryPending,
			NextAttemptAt:  now,
		})
	}
	if f.cfg.DB != nil {
		return f.cfg.DB.Tx(ctx, func(tx *db.Tx) error {
			return f.cfg.DeliveryStore.Enqueue(ctx, tx, deliveries)
		})
	}
	return f.cfg.DeliveryStore.Enqueue(ctx, nil, deliveries)
}
