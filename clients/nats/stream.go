package natsclient

import (
	"time"

	"github.com/nats-io/nats.go"
)

// Storage selects JetStream's storage backend.
type Storage int

const (
	StorageFile   Storage = iota // default — persistent across server restart
	StorageMemory                // ephemeral
)

// Retention selects when JetStream discards messages.
type Retention int

const (
	RetentionLimits    Retention = iota // default — keep until size/age/count limit hit
	RetentionInterest                   // keep only while there are interested consumers
	RetentionWorkQueue                  // delete after consumer ack (one-shot queue semantics)
)

// StreamConfig is the kit's stream descriptor — translated to a nats.StreamConfig
// internally. Zero values for limits mean "no limit".
type StreamConfig struct {
	Name      string        // e.g. "ORDERS"
	Subjects  []string      // wildcards OK: ["orders.>", "orders.*.created"]
	Storage   Storage       // default StorageFile
	Retention Retention     // default RetentionLimits
	MaxAge    time.Duration // 0 = unlimited
	MaxBytes  int64         // 0 = unlimited
	MaxMsgs   int64         // 0 = unlimited
	Replicas  int           // 0 → 1 (single-replica)
	Dedup     time.Duration // server-side dedup window via Nats-Msg-Id header; 0 = off
}

func storageToNats(s Storage) nats.StorageType {
	if s == StorageMemory {
		return nats.MemoryStorage
	}
	return nats.FileStorage
}

func retentionToNats(r Retention) nats.RetentionPolicy {
	switch r {
	case RetentionInterest:
		return nats.InterestPolicy
	case RetentionWorkQueue:
		return nats.WorkQueuePolicy
	default:
		return nats.LimitsPolicy
	}
}

func buildNatsStreamConfig(cfg StreamConfig) *nats.StreamConfig {
	replicas := cfg.Replicas
	if replicas == 0 {
		replicas = 1
	}
	return &nats.StreamConfig{
		Name:       cfg.Name,
		Subjects:   cfg.Subjects,
		Storage:    storageToNats(cfg.Storage),
		Retention:  retentionToNats(cfg.Retention),
		MaxAge:     cfg.MaxAge,
		MaxBytes:   cfg.MaxBytes,
		MaxMsgs:    cfg.MaxMsgs,
		Replicas:   replicas,
		Duplicates: cfg.Dedup,
	}
}
