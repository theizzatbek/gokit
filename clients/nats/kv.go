package natsclient

import (
	"context"
	"errors"
	"time"

	"github.com/nats-io/nats.go"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// KVConfig describes one JetStream KV bucket.
//
// Bucket name is the only required field. TTL `History` defaults to 1
// (latest value only) — set higher to retain N revisions per key.
// `MaxValueSize` 0 = unlimited.
type KVConfig struct {
	Bucket       string
	Description  string
	History      uint8         // 0 → 1 = latest revision only
	TTL          time.Duration // per-key TTL; 0 = no expiry
	MaxValueSize int32         // 0 = unlimited
	Replicas     int           // 0 → 1
	Storage      Storage       // default StorageFile
}

// EnsureKVBucket creates the bucket if absent, returns the underlying
// nats.KeyValue handle. Idempotent — safe on every startup.
func (c *Client) EnsureKVBucket(ctx context.Context, cfg KVConfig) (nats.KeyValue, error) {
	if cfg.Bucket == "" {
		return nil, xerrs.Validation(CodeStreamConfigInvalid, "natsclient: KVConfig.Bucket required")
	}
	if cfg.Replicas == 0 {
		cfg.Replicas = 1
	}
	if cfg.History == 0 {
		cfg.History = 1
	}
	jsCfg := &nats.KeyValueConfig{
		Bucket:       cfg.Bucket,
		Description:  cfg.Description,
		History:      cfg.History,
		TTL:          cfg.TTL,
		MaxValueSize: cfg.MaxValueSize,
		Replicas:     cfg.Replicas,
		Storage:      storageToNats(cfg.Storage),
	}
	kv, err := c.js.KeyValue(cfg.Bucket)
	if err == nil {
		return kv, nil
	}
	if !errors.Is(err, nats.ErrBucketNotFound) {
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeKVOpFailed, "natsclient: kv lookup")
	}
	kv, err = c.js.CreateKeyValue(jsCfg)
	if err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeKVOpFailed, "natsclient: kv create")
	}
	if c.opts.logger != nil {
		c.opts.logger.Info("nats kv bucket created", "bucket", cfg.Bucket)
	}
	return kv, nil
}

// KV is the typed handle for one KV bucket. Values pass through the
// client's codec (JSON by default). Use as the type-safe alternative
// to the raw nats.KeyValue API for app-level config/state.
type KV[T any] struct {
	c     *Client
	kv    nats.KeyValue
	codec Codec
}

// NewKV returns a typed KV handle for an existing bucket. Use
// EnsureKVBucket first if the bucket may not exist yet.
func NewKV[T any](c *Client, bucket string) (*KV[T], error) {
	kv, err := c.js.KeyValue(bucket)
	if err != nil {
		if errors.Is(err, nats.ErrBucketNotFound) {
			return nil, xerrs.Wrapf(err, xerrs.KindNotFound, CodeKVKeyNotFound,
				"natsclient: kv bucket %q not found", bucket)
		}
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeKVOpFailed, "natsclient: kv lookup")
	}
	return &KV[T]{c: c, kv: kv, codec: c.opts.codec}, nil
}

// Get returns the typed value for key. Returns *errs.Error{KindNotFound,
// CodeKVKeyNotFound} on miss.
func (k *KV[T]) Get(_ context.Context, key string) (T, uint64, error) {
	var zero T
	entry, err := k.kv.Get(key)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return zero, 0, xerrs.Wrapf(err, xerrs.KindNotFound, CodeKVKeyNotFound,
				"natsclient: kv key %q not found", key)
		}
		return zero, 0, xerrs.Wrap(err, xerrs.KindUnavailable, CodeKVOpFailed, "natsclient: kv get")
	}
	var v T
	if err := k.codec.Unmarshal(entry.Value(), &v); err != nil {
		return zero, 0, xerrs.Wrap(err, xerrs.KindValidation, CodeDecodeFailed, "natsclient: kv decode")
	}
	return v, entry.Revision(), nil
}

// Put writes value at key. Returns the new revision number.
func (k *KV[T]) Put(_ context.Context, key string, v T) (uint64, error) {
	body, err := k.codec.Marshal(v)
	if err != nil {
		return 0, xerrs.Wrap(err, xerrs.KindValidation, CodeEncodeFailed, "natsclient: kv encode")
	}
	rev, err := k.kv.Put(key, body)
	if err != nil {
		return 0, xerrs.Wrap(err, xerrs.KindUnavailable, CodeKVOpFailed, "natsclient: kv put")
	}
	return rev, nil
}

// Update writes value at key only when the last-seen revision matches —
// the JetStream compare-and-swap primitive. Returns *errs.Error of
// KindConflict when the revision drifted.
func (k *KV[T]) Update(_ context.Context, key string, v T, lastRev uint64) (uint64, error) {
	body, err := k.codec.Marshal(v)
	if err != nil {
		return 0, xerrs.Wrap(err, xerrs.KindValidation, CodeEncodeFailed, "natsclient: kv encode")
	}
	rev, err := k.kv.Update(key, body, lastRev)
	if err != nil {
		return 0, xerrs.Wrap(err, xerrs.KindConflict, CodeKVOpFailed, "natsclient: kv update conflict")
	}
	return rev, nil
}

// Delete tombstones the key — future Get returns NotFound.
func (k *KV[T]) Delete(_ context.Context, key string) error {
	if err := k.kv.Delete(key); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeKVOpFailed, "natsclient: kv delete")
	}
	return nil
}

// Raw returns the underlying nats.KeyValue for advanced ops (Watch,
// History, ListKeys). Errors via this path are NOT wrapped through
// *errs.Error.
func (k *KV[T]) Raw() nats.KeyValue { return k.kv }
