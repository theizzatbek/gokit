package webhooks

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/theizzatbek/gokit/db"
)

// DeliveryStatus is the lifecycle state of a single Delivery.
type DeliveryStatus string

const (
	DeliveryPending   DeliveryStatus = "pending"
	DeliveryDelivered DeliveryStatus = "delivered"
	DeliveryDLQ       DeliveryStatus = "dlq"
)

// Delivery is one fan-out row: one inbound Event multiplied by one
// matching Subscription. Worker drains rows with status=pending where
// next_attempt_at <= now() and updates them via DeliveryStore.
type Delivery struct {
	ID             uuid.UUID
	SubscriptionID uuid.UUID
	EventID        uuid.UUID
	EventType      string
	Payload        []byte
	Headers        map[string][]string
	Attempts       int
	Status         DeliveryStatus
	NextAttemptAt  time.Time
	LastStatusCode int
	LastError      string
	DeliveredAt    *time.Time
	CreatedAt      time.Time

	// TargetURL / Secret are NOT persisted on the delivery row —
	// they come from the joined Subscription at Claim time. Stored
	// here in-memory so Worker doesn't have to round-trip per row.
	TargetURL string
	Secret    string
}

// DeliveryStore is the persistence contract for delivery rows.
// Enqueue accepts a db.Querier so Fanout can write in the same
// transaction as the pg_notify call.
type DeliveryStore interface {
	Enqueue(ctx context.Context, q db.Querier, deliveries []Delivery) error
	Claim(ctx context.Context, batchSize int) ([]Delivery, error)
	MarkDelivered(ctx context.Context, id uuid.UUID, statusCode int) error
	MarkFailed(ctx context.Context, id uuid.UUID, statusCode int,
		errMsg string, nextAttemptAt time.Time) error
	MarkDLQ(ctx context.Context, id uuid.UUID, statusCode int, errMsg string) error
}
