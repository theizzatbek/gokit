package storepg

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/theizzatbek/gokit/clients/webhooks"
	"github.com/theizzatbek/gokit/db"
)

// DeliveryStore is the Postgres-backed DeliveryStore.
type DeliveryStore struct {
	q  db.Querier
	cr *crypto
}

// NewDeliveryStore wires a DeliveryStore. secretKey must be the same
// 32-byte AES key that NewSubStore was given — Claim joins
// webhook_subscriptions.secret_enc and decrypts it inline so the
// Worker doesn't need a second round-trip per row.
func NewDeliveryStore(q db.Querier, secretKey []byte) (*DeliveryStore, error) {
	cr, err := newCrypto(secretKey)
	if err != nil {
		return nil, err
	}
	return &DeliveryStore{q: q, cr: cr}, nil
}

// NotifyChannel is the LISTEN channel pg_notify'd by Enqueue.
const NotifyChannel = "webhook_deliveries_new"

// Enqueue inserts deliveries inside the caller's Querier and fires
// pg_notify on commit. ON CONFLICT DO NOTHING covers the JetStream-
// redeliver idempotency case.
//
// When Delivery.NextAttemptAt is zero or within 1 second of the
// current wall clock, the column is left to its SQL DEFAULT (now())
// so Postgres's own clock governs "due" comparisons — this avoids
// false negatives from sub-second Go/Postgres clock skew.
func (ds *DeliveryStore) Enqueue(ctx context.Context, q db.Querier, deliveries []webhooks.Delivery) error {
	if len(deliveries) == 0 {
		return nil
	}

	type row struct {
		subID     any
		eventID   any
		eventType any
		payload   any
		headers   any
		nat       any // next_attempt_at; nil means use SQL DEFAULT
	}

	rows := make([]row, 0, len(deliveries))
	for _, d := range deliveries {
		headersJSON, err := json.Marshal(d.Headers)
		if err != nil {
			return err
		}
		var nat any
		if !d.NextAttemptAt.IsZero() && time.Until(d.NextAttemptAt) > time.Second {
			nat = d.NextAttemptAt
		} // else nil → SQL DEFAULT (now())
		rows = append(rows, row{d.SubscriptionID, d.EventID, d.EventType, d.Payload, headersJSON, nat})
	}

	// Build the VALUES clause. Rows where nat==nil use DEFAULT for
	// the next_attempt_at column.
	args := make([]any, 0, 5*len(rows))
	values := ""
	for i, r := range rows {
		if i > 0 {
			values += ","
		}
		base := len(args) + 1
		if r.nat != nil {
			values += "($" + strconv.Itoa(base) + ",$" + strconv.Itoa(base+1) +
				",$" + strconv.Itoa(base+2) + ",$" + strconv.Itoa(base+3) +
				",$" + strconv.Itoa(base+4) + ",$" + strconv.Itoa(base+5) + ")"
			args = append(args, r.subID, r.eventID, r.eventType, r.payload, r.headers, r.nat)
		} else {
			values += "($" + strconv.Itoa(base) + ",$" + strconv.Itoa(base+1) +
				",$" + strconv.Itoa(base+2) + ",$" + strconv.Itoa(base+3) +
				",$" + strconv.Itoa(base+4) + ",DEFAULT)"
			args = append(args, r.subID, r.eventID, r.eventType, r.payload, r.headers)
		}
	}
	sql := `
		WITH ins AS (
			INSERT INTO webhook_deliveries
				(subscription_id, event_id, event_type, payload, headers, next_attempt_at)
			VALUES ` + values + `
			ON CONFLICT (subscription_id, event_id) DO NOTHING
			RETURNING 1
		)
		SELECT pg_notify('` + NotifyChannel + `', '') FROM ins
	`
	_, err := q.Exec(ctx, sql, args...)
	return err
}

// Claim selects up to batchSize pending rows whose next_attempt_at
// is in the past, joins the owning subscription, decrypts the
// secret, and returns hydrated Delivery values.
//
// Uses FOR UPDATE SKIP LOCKED inside its own short transaction so
// concurrent Worker pods don't double-claim the same row.
func (ds *DeliveryStore) Claim(ctx context.Context, batchSize int) ([]webhooks.Delivery, error) {
	if batchSize <= 0 {
		batchSize = 32
	}
	// Extract the underlying *pgxpool.Pool via the Pool() method on *db.DB.
	type pooler interface {
		Pool() *pgxpool.Pool
	}
	if p, ok := ds.q.(pooler); ok {
		pool := p.Pool()
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return nil, err
		}
		defer tx.Commit(ctx) //nolint:errcheck
		return ds.claimQuery(ctx, tx, batchSize)
	}
	// Querier is already a transaction (e.g. *db.Tx passed by caller).
	// pgx.Tx satisfies db.Querier directly — pass through as-is.
	return ds.claimQuery(ctx, ds.q, batchSize)
}

func (ds *DeliveryStore) claimQuery(ctx context.Context, q db.Querier, batchSize int) ([]webhooks.Delivery, error) {
	rows, err := q.Query(ctx, `
		SELECT d.id, d.subscription_id, d.event_id, d.event_type,
		       d.payload, d.headers, d.attempts, d.status,
		       d.next_attempt_at, COALESCE(d.last_status_code, 0),
		       d.last_error, d.delivered_at, d.created_at,
		       s.target_url, s.secret_enc
		FROM webhook_deliveries d
		JOIN webhook_subscriptions s ON s.id = d.subscription_id
		WHERE d.status = 'pending' AND d.next_attempt_at <= now()
		ORDER BY d.next_attempt_at
		LIMIT $1
		FOR UPDATE OF d SKIP LOCKED
	`, batchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []webhooks.Delivery
	for rows.Next() {
		var (
			d           webhooks.Delivery
			headersJSON []byte
			status      string
			encSecret   []byte
			deliveredAt *time.Time
		)
		if err := rows.Scan(&d.ID, &d.SubscriptionID, &d.EventID, &d.EventType,
			&d.Payload, &headersJSON, &d.Attempts, &status,
			&d.NextAttemptAt, &d.LastStatusCode, &d.LastError,
			&deliveredAt, &d.CreatedAt, &d.TargetURL, &encSecret); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(headersJSON, &d.Headers)
		d.Status = webhooks.DeliveryStatus(status)
		d.DeliveredAt = deliveredAt
		plain, err := ds.cr.open(encSecret)
		if err != nil {
			return nil, err
		}
		d.Secret = string(plain)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (ds *DeliveryStore) MarkDelivered(ctx context.Context, id uuid.UUID, statusCode int) error {
	_, err := ds.q.Exec(ctx, `
		UPDATE webhook_deliveries
		SET status = 'delivered', last_status_code = $2,
		    last_error = '', delivered_at = now()
		WHERE id = $1
	`, id, statusCode)
	return err
}

func (ds *DeliveryStore) MarkFailed(ctx context.Context, id uuid.UUID, statusCode int, errMsg string, nextAttemptAt time.Time) error {
	_, err := ds.q.Exec(ctx, `
		UPDATE webhook_deliveries
		SET attempts = attempts + 1, last_status_code = $2,
		    last_error = $3, next_attempt_at = $4
		WHERE id = $1
	`, id, statusCode, truncate(errMsg, 4096), nextAttemptAt)
	return err
}

func (ds *DeliveryStore) MarkDLQ(ctx context.Context, id uuid.UUID, statusCode int, errMsg string) error {
	_, err := ds.q.Exec(ctx, `
		UPDATE webhook_deliveries
		SET status = 'dlq', attempts = attempts + 1,
		    last_status_code = $2, last_error = $3
		WHERE id = $1
	`, id, statusCode, truncate(errMsg, 4096))
	return err
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
