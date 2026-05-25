package natsclient

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestBuildNatsStreamConfig_MapsAllFields(t *testing.T) {
	cfg := StreamConfig{
		Name:      "ORDERS",
		Subjects:  []string{"orders.>", "orders.*.created"},
		Storage:   StorageMemory,
		Retention: RetentionWorkQueue,
		MaxAge:    24 * time.Hour,
		MaxBytes:  1024 * 1024,
		MaxMsgs:   1000,
		Replicas:  3,
		Dedup:     time.Minute,
	}
	got := buildNatsStreamConfig(cfg)
	want := &nats.StreamConfig{
		Name:       "ORDERS",
		Subjects:   []string{"orders.>", "orders.*.created"},
		Storage:    nats.MemoryStorage,
		Retention:  nats.WorkQueuePolicy,
		MaxAge:     24 * time.Hour,
		MaxBytes:   1024 * 1024,
		MaxMsgs:    1000,
		Replicas:   3,
		Duplicates: time.Minute,
	}
	if !equalStreamConfig(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestBuildNatsStreamConfig_StorageFileDefault(t *testing.T) {
	got := buildNatsStreamConfig(StreamConfig{Name: "X", Subjects: []string{"x.>"}})
	if got.Storage != nats.FileStorage {
		t.Fatalf("default Storage = %v, want FileStorage", got.Storage)
	}
	if got.Retention != nats.LimitsPolicy {
		t.Fatalf("default Retention = %v, want LimitsPolicy", got.Retention)
	}
	if got.Replicas != 1 {
		t.Fatalf("default Replicas = %d, want 1", got.Replicas)
	}
}

// equalStreamConfig compares only the fields we map.
func equalStreamConfig(a, b *nats.StreamConfig) bool {
	if a.Name != b.Name || a.Storage != b.Storage || a.Retention != b.Retention {
		return false
	}
	if a.MaxAge != b.MaxAge || a.MaxBytes != b.MaxBytes || a.MaxMsgs != b.MaxMsgs {
		return false
	}
	if a.Replicas != b.Replicas || a.Duplicates != b.Duplicates {
		return false
	}
	if len(a.Subjects) != len(b.Subjects) {
		return false
	}
	for i := range a.Subjects {
		if a.Subjects[i] != b.Subjects[i] {
			return false
		}
	}
	return true
}
