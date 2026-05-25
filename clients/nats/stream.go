package natsclient

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	xerrs "github.com/theizzatbek/gokit/errs"
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

// EnsureStream creates the stream if absent, updates it if config drifted.
// Idempotent and safe to call on every startup before publishing/subscribing.
func (c *Client) EnsureStream(ctx context.Context, cfg StreamConfig) error {
	if err := validateStreamConfig(cfg); err != nil {
		return err
	}
	nCfg := buildNatsStreamConfig(cfg)
	existing, err := c.js.StreamInfo(cfg.Name)
	switch {
	case err == nil:
		if !streamConfigMatches(&existing.Config, nCfg) {
			if _, err := c.js.UpdateStream(nCfg); err != nil {
				return classifyStreamErr(err, "update")
			}
			if c.opts.logger != nil {
				c.opts.logger.Info("nats stream updated", "stream", cfg.Name)
			}
		}
		return nil
	case errors.Is(err, nats.ErrStreamNotFound):
		if _, err := c.js.AddStream(nCfg); err != nil {
			return classifyStreamErr(err, "create")
		}
		if c.opts.logger != nil {
			c.opts.logger.Info("nats stream created", "stream", cfg.Name)
		}
		return nil
	default:
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeStreamOpFailed, "natsclient: stream lookup")
	}
}

// DeleteStream removes a stream by name. Idempotent: missing stream is not an error.
func (c *Client) DeleteStream(ctx context.Context, name string) error {
	if err := c.js.DeleteStream(name); err != nil {
		if errors.Is(err, nats.ErrStreamNotFound) {
			return nil
		}
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeStreamOpFailed, "natsclient: stream delete")
	}
	return nil
}

// validateStreamConfig performs caller-side validation so we surface a clean
// *errs.Error before round-tripping to the server. Some servers (notably
// nats:2 with default settings) accept under-specified configs by auto-filling
// Subjects from Name — that behavior is server-version dependent, so the kit
// enforces the contract here.
func validateStreamConfig(cfg StreamConfig) error {
	if strings.TrimSpace(cfg.Name) == "" {
		return xerrs.Validation(CodeStreamConfigInvalid, "natsclient: stream Name is required")
	}
	if len(cfg.Subjects) == 0 {
		return xerrs.Validation(CodeStreamConfigInvalid, "natsclient: stream Subjects must be non-empty")
	}
	for _, s := range cfg.Subjects {
		if strings.TrimSpace(s) == "" {
			return xerrs.Validation(CodeStreamConfigInvalid, "natsclient: stream Subjects must not contain empty entries")
		}
	}
	return nil
}

// streamConfigMatches checks whether the existing stream config matches the
// requested one for the fields the kit manages.
func streamConfigMatches(have, want *nats.StreamConfig) bool {
	if have.Name != want.Name || have.Storage != want.Storage || have.Retention != want.Retention {
		return false
	}
	if have.MaxAge != want.MaxAge || have.MaxBytes != want.MaxBytes || have.MaxMsgs != want.MaxMsgs {
		return false
	}
	if have.Replicas != want.Replicas || have.Duplicates != want.Duplicates {
		return false
	}
	if len(have.Subjects) != len(want.Subjects) {
		return false
	}
	for i := range have.Subjects {
		if have.Subjects[i] != want.Subjects[i] {
			return false
		}
	}
	return true
}

// classifyStreamErr wraps an op error into either KindValidation or KindUnavailable
// based on substring matching against common NATS validation-style messages.
func classifyStreamErr(err error, op string) error {
	if err == nil {
		return nil
	}
	low := strings.ToLower(err.Error())
	for _, sub := range []string{"invalid", "required", "must", "missing"} {
		if strings.Contains(low, sub) {
			return xerrs.Wrapf(err, xerrs.KindValidation, CodeStreamConfigInvalid, "natsclient: stream %s rejected", op)
		}
	}
	return xerrs.Wrapf(err, xerrs.KindUnavailable, CodeStreamOpFailed, "natsclient: stream %s", op)
}
