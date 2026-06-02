package storepg

import (
	"context"
	_ "embed"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/theizzatbek/gokit/clients/webhooks"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

//go:embed schema.sql
var embeddedSchema string

// Schema returns the embedded DDL for the subscriptions + deliveries
// tables. Callers apply it through db/migrate or any other migration
// tool.
func Schema() string { return embeddedSchema }

// SubStore is the Postgres-backed SubscriptionStore.
type SubStore struct {
	q  db.Querier
	cr *crypto
}

// NewSubStore wires a SubStore to the kit's *db.DB (or any
// db.Querier). secretKey is the 32-byte AES key used to seal/open
// the secret column.
func NewSubStore(q db.Querier, secretKey []byte) (*SubStore, error) {
	cr, err := newCrypto(secretKey)
	if err != nil {
		return nil, err
	}
	return &SubStore{q: q, cr: cr}, nil
}

var subColumns = "id, owner_id, target_url, secret_enc, event_types, status, description, created_at, updated_at"

func (s *SubStore) Create(ctx context.Context, sub webhooks.Subscription) (webhooks.Subscription, error) {
	if sub.Secret == "" {
		return webhooks.Subscription{}, errs.Validation(webhooks.CodeMissingSecret, "Subscription.Secret required")
	}
	if len(sub.EventTypes) == 0 {
		return webhooks.Subscription{}, errs.Validation(webhooks.CodeInvalidEventTypes, "Subscription.EventTypes required")
	}
	if sub.Status == "" {
		sub.Status = webhooks.SubscriptionActive
	}
	enc, err := s.cr.seal([]byte(sub.Secret))
	if err != nil {
		return webhooks.Subscription{}, err
	}
	row := s.q.QueryRow(ctx, `
		INSERT INTO webhook_subscriptions
			(owner_id, target_url, secret_enc, event_types, status, description)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+subColumns,
		sub.OwnerID, sub.TargetURL, enc, sub.EventTypes, string(sub.Status), sub.Description,
	)
	return s.scanRow(row)
}

func (s *SubStore) Get(ctx context.Context, id uuid.UUID) (webhooks.Subscription, error) {
	row := s.q.QueryRow(ctx, `SELECT `+subColumns+` FROM webhook_subscriptions WHERE id = $1`, id)
	sub, err := s.scanRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return webhooks.Subscription{}, errs.NotFoundf(webhooks.CodeSubscriptionNotFound, "subscription %s not found", id)
	}
	return sub, err
}

func (s *SubStore) List(ctx context.Context, ownerID string) ([]webhooks.Subscription, error) {
	rows, err := s.q.Query(ctx, `SELECT `+subColumns+` FROM webhook_subscriptions WHERE owner_id = $1 AND status = 'active' ORDER BY created_at`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanRows(rows)
}

func (s *SubStore) ListByEvent(ctx context.Context, eventType string) ([]webhooks.Subscription, error) {
	rows, err := s.q.Query(ctx, `SELECT `+subColumns+` FROM webhook_subscriptions WHERE $1 = ANY(event_types) AND status = 'active'`, eventType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanRows(rows)
}

func (s *SubStore) Update(ctx context.Context, sub webhooks.Subscription) (webhooks.Subscription, error) {
	enc, err := s.cr.seal([]byte(sub.Secret))
	if err != nil {
		return webhooks.Subscription{}, err
	}
	row := s.q.QueryRow(ctx, `
		UPDATE webhook_subscriptions
		SET target_url = $2, secret_enc = $3, event_types = $4,
		    status = $5, description = $6, updated_at = now()
		WHERE id = $1
		RETURNING `+subColumns,
		sub.ID, sub.TargetURL, enc, sub.EventTypes, string(sub.Status), sub.Description,
	)
	out, err := s.scanRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return webhooks.Subscription{}, errs.NotFoundf(webhooks.CodeSubscriptionNotFound, "subscription %s not found", sub.ID)
	}
	return out, err
}

func (s *SubStore) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.q.Exec(ctx, `UPDATE webhook_subscriptions SET status = 'disabled', updated_at = now() WHERE id = $1`, id)
	return err
}

func (s *SubStore) scanRow(row pgx.Row) (webhooks.Subscription, error) {
	var (
		sub       webhooks.Subscription
		encSecret []byte
		status    string
		createdAt time.Time
		updatedAt time.Time
	)
	if err := row.Scan(&sub.ID, &sub.OwnerID, &sub.TargetURL, &encSecret,
		&sub.EventTypes, &status, &sub.Description, &createdAt, &updatedAt); err != nil {
		return webhooks.Subscription{}, err
	}
	plaintext, err := s.cr.open(encSecret)
	if err != nil {
		return webhooks.Subscription{}, err
	}
	sub.Secret = string(plaintext)
	sub.Status = webhooks.Status(status)
	sub.CreatedAt = createdAt
	sub.UpdatedAt = updatedAt
	return sub, nil
}

func (s *SubStore) scanRows(rows pgx.Rows) ([]webhooks.Subscription, error) {
	var out []webhooks.Subscription
	for rows.Next() {
		sub, err := s.scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}
