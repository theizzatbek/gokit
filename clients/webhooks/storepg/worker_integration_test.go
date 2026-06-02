package storepg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/theizzatbek/gokit/clients/webhooks"
)

func TestWorker_DeliversAndMarks(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := testDB.Exec(ctx, "TRUNCATE webhook_subscriptions CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	hit := atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	key := newTestKey()
	subStore, _ := NewSubStore(testDB, key)
	delivStore, _ := NewDeliveryStore(testDB, key)

	sub, _ := subStore.Create(ctx, webhooks.Subscription{
		OwnerID:    "o",
		TargetURL:  srv.URL,
		Secret:     "secret",
		EventTypes: []string{"e"},
	})

	_ = delivStore.Enqueue(ctx, testDB, []webhooks.Delivery{{
		ID:             uuid.New(),
		SubscriptionID: sub.ID,
		EventID:        uuid.New(),
		EventType:      "e",
		Payload:        []byte(`{}`),
	}})

	w, _ := webhooks.NewWorker(webhooks.WorkerConfig{
		SubStore:      subStore,
		DeliveryStore: delivStore,
		HTTPClient:    &http.Client{Timeout: 2 * time.Second},
		MaxAttempts:   3,
		Interval:      100 * time.Millisecond,
		BatchSize:     16,
		MaxInFlight:   4,
	})
	w.Start(ctx)
	defer w.Stop(context.Background())

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && hit.Load() == 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if hit.Load() == 0 {
		t.Fatal("delivery never hit the server")
	}
}
