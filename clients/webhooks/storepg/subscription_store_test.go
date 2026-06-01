package storepg

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/theizzatbek/gokit/clients/webhooks"
)

func TestSubscriptionStore_CRUD(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(ctx, "TRUNCATE webhook_subscriptions CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	s, err := NewSubStore(testDB, newTestKey())
	if err != nil {
		t.Fatalf("NewSubStore: %v", err)
	}

	created, err := s.Create(ctx, webhooks.Subscription{
		OwnerID:    "owner-1",
		TargetURL:  "https://example.com/hook",
		Secret:     "shhh",
		EventTypes: []string{"link.created"},
		Status:     webhooks.SubscriptionActive,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("Create returned nil ID")
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Secret != "shhh" {
		t.Fatalf("Secret roundtrip failed: %q", got.Secret)
	}

	list, err := s.List(ctx, "owner-1")
	if err != nil || len(list) != 1 {
		t.Fatalf("List: err=%v len=%d", err, len(list))
	}

	byEvent, err := s.ListByEvent(ctx, "link.created")
	if err != nil || len(byEvent) != 1 {
		t.Fatalf("ListByEvent: err=%v len=%d", err, len(byEvent))
	}

	created.TargetURL = "https://example.com/hook-v2"
	updated, err := s.Update(ctx, created)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.TargetURL != "https://example.com/hook-v2" {
		t.Fatalf("Update did not persist: %s", updated.TargetURL)
	}

	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	after, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if after.Status != webhooks.SubscriptionDisabled {
		t.Fatalf("Delete should soft-disable, got status=%s", after.Status)
	}
}
