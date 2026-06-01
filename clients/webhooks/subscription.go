package webhooks

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Status is the lifecycle state of a Subscription.
type Status string

const (
	SubscriptionActive   Status = "active"
	SubscriptionDisabled Status = "disabled"
)

// Subscription is a webhook target owned by some external principal
// (Subscription.OwnerID — opaque string, neutral to the caller's
// tenant model). EventTypes is the closed list of bus subjects the
// subscriber wants delivered.
//
// Secret is plaintext in memory. The storepg backend encrypts it
// at rest via AES-256-GCM (see storepg/crypto.go); callers that
// implement their own SubscriptionStore are responsible for secret
// confidentiality.
type Subscription struct {
	ID          uuid.UUID
	OwnerID     string
	TargetURL   string
	Secret      string
	EventTypes  []string
	Status      Status
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// LogValue masks Secret so subscription objects logged via slog
// don't leak the HMAC key.
func (s Subscription) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", s.ID.String()),
		slog.String("owner_id", s.OwnerID),
		slog.String("target_url", s.TargetURL),
		slog.Any("event_types", s.EventTypes),
		slog.String("status", string(s.Status)),
		slog.String("secret", "***"),
	)
}

// SubscriptionStore is the persistence contract for webhook
// subscriptions. Implementations live in storepg (Postgres) or any
// caller-supplied backend.
type SubscriptionStore interface {
	Create(ctx context.Context, s Subscription) (Subscription, error)
	Get(ctx context.Context, id uuid.UUID) (Subscription, error)
	List(ctx context.Context, ownerID string) ([]Subscription, error)
	ListByEvent(ctx context.Context, eventType string) ([]Subscription, error)
	Update(ctx context.Context, s Subscription) (Subscription, error)
	Delete(ctx context.Context, id uuid.UUID) error // soft-delete: status=disabled
}
