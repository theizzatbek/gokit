package storepg

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/theizzatbek/gokit/clients/webhooks"
)

func TestDeliveryStore_EnqueueClaimMark(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(ctx, "TRUNCATE webhook_subscriptions CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	key := newTestKey()
	subStore, _ := NewSubStore(testDB, key)
	sub, err := subStore.Create(ctx, webhooks.Subscription{
		OwnerID:    "owner",
		TargetURL:  "https://example.com/h",
		Secret:     "s",
		EventTypes: []string{"x.y"},
	})
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}

	ds, err := NewDeliveryStore(testDB, key)
	if err != nil {
		t.Fatalf("NewDeliveryStore: %v", err)
	}

	eventID := uuid.New()
	deliveries := []webhooks.Delivery{{
		SubscriptionID: sub.ID,
		EventID:        eventID,
		EventType:      "x.y",
		Payload:        []byte(`{}`),
		NextAttemptAt:  time.Now(),
	}}
	if err := ds.Enqueue(ctx, testDB, deliveries); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Idempotency: re-enqueue with the same event_id must be a no-op
	if err := ds.Enqueue(ctx, testDB, deliveries); err != nil {
		t.Fatalf("Enqueue dup: %v", err)
	}

	claimed, err := ds.Claim(ctx, 10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("Claim returned %d rows", len(claimed))
	}
	if claimed[0].TargetURL != sub.TargetURL {
		t.Fatalf("Claim should join TargetURL: got %q", claimed[0].TargetURL)
	}
	if claimed[0].Secret != "s" {
		t.Fatalf("Claim should join+decrypt Secret: got %q", claimed[0].Secret)
	}

	if err := ds.MarkDelivered(ctx, claimed[0].ID, 200); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}

	// Wait briefly to ensure next_attempt_at ordering doesn't re-surface row.
	time.Sleep(50 * time.Millisecond)

	again, _ := ds.Claim(ctx, 10)
	if len(again) != 0 {
		t.Fatalf("delivered rows must not be re-claimed, got %d", len(again))
	}
}
