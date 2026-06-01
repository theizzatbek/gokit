package webhooks

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/theizzatbek/gokit/db"
)

type fakeSubStore struct{ subs []Subscription }

func (f *fakeSubStore) Create(context.Context, Subscription) (Subscription, error) {
	return Subscription{}, nil
}
func (f *fakeSubStore) Get(context.Context, uuid.UUID) (Subscription, error) {
	return Subscription{}, nil
}
func (f *fakeSubStore) List(context.Context, string) ([]Subscription, error) { return nil, nil }
func (f *fakeSubStore) ListByEvent(_ context.Context, ev string) ([]Subscription, error) {
	var out []Subscription
	for _, s := range f.subs {
		for _, e := range s.EventTypes {
			if e == ev {
				out = append(out, s)
			}
		}
	}
	return out, nil
}
func (f *fakeSubStore) Update(context.Context, Subscription) (Subscription, error) {
	return Subscription{}, nil
}
func (f *fakeSubStore) Delete(context.Context, uuid.UUID) error { return nil }

type fakeDeliveryStore struct {
	mu       sync.Mutex
	enqueued []Delivery
}

func (f *fakeDeliveryStore) Enqueue(_ context.Context, _ db.Querier, ds []Delivery) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, ds...)
	return nil
}
func (f *fakeDeliveryStore) Claim(context.Context, int) ([]Delivery, error) { return nil, nil }
func (f *fakeDeliveryStore) MarkDelivered(context.Context, uuid.UUID, int) error {
	return nil
}
func (f *fakeDeliveryStore) MarkFailed(context.Context, uuid.UUID, int, string, time.Time) error {
	return nil
}
func (f *fakeDeliveryStore) MarkDLQ(context.Context, uuid.UUID, int, string) error { return nil }

func TestFanout_BuildsOneDeliveryPerMatchingSub(t *testing.T) {
	subs := &fakeSubStore{subs: []Subscription{
		{ID: uuid.New(), EventTypes: []string{"a"}, Secret: "x", TargetURL: "https://a"},
		{ID: uuid.New(), EventTypes: []string{"a"}, Secret: "y", TargetURL: "https://b"},
		{ID: uuid.New(), EventTypes: []string{"b"}, Secret: "z", TargetURL: "https://c"},
	}}
	store := &fakeDeliveryStore{}

	f, err := NewFanout(FanoutConfig{
		SubStore:      subs,
		DeliveryStore: store,
	})
	if err != nil {
		t.Fatalf("NewFanout: %v", err)
	}

	if err := f.HandleEvent(context.Background(), Event{
		ID:        uuid.New(),
		EventType: "a",
		Payload:   []byte(`{}`),
	}); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}

	if len(store.enqueued) != 2 {
		t.Fatalf("want 2 deliveries (subscribers to 'a'), got %d", len(store.enqueued))
	}
}
