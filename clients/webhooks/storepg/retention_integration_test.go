package storepg

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/theizzatbek/gokit/clients/webhooks"
)

func TestRetention_DeletesOldDelivered(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(ctx, "TRUNCATE webhook_subscriptions CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	key := newTestKey()
	subStore, _ := NewSubStore(testDB, key)
	sub, _ := subStore.Create(ctx, webhooks.Subscription{
		OwnerID: "o", TargetURL: "https://x", Secret: "s", EventTypes: []string{"e"},
	})

	// Insert an already-delivered row with delivered_at in the deep past.
	if _, err := testDB.Exec(ctx, `
		INSERT INTO webhook_deliveries
			(id, subscription_id, event_id, event_type, payload, status, delivered_at)
		VALUES ($1, $2, $3, 'e', $4, 'delivered', now() - interval '60 days')
	`, uuid.New(), sub.ID, uuid.New(), []byte(`{}`)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := webhooks.NewRetentionWorker(webhooks.RetentionConfig{
		DB:       testDB,
		TTL:      30 * 24 * time.Hour,
		Interval: 50 * time.Millisecond,
	})
	r.Start(ctx)
	defer r.Stop(context.Background())

	deadline := time.Now().Add(3 * time.Second)
	var count int
	for time.Now().Before(deadline) {
		row := testDB.QueryRow(ctx, `SELECT count(*) FROM webhook_deliveries`)
		_ = row.Scan(&count)
		if count == 0 {
			return
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatalf("expected row to be retention-deleted; count=%d", count)
}
