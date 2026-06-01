package s3client

import (
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

// Option configures [Connect].
type Option func(*options)

type options struct {
	logger  *slog.Logger
	metrics *metricsCollector
}

// WithLogger wires a *slog.Logger for per-operation diagnostics.
// Without it the client runs silently.
//
// Levels emitted:
//   - Debug: every successful op (key + size + elapsed).
//   - Warn:  operation errors (transport-level + mapped *errs.Error).
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.logger = l }
}

// WithMetrics registers Prometheus collectors on reg:
//   - s3_operations_total{op,outcome}            (counter)
//   - s3_operation_duration_seconds{op}          (histogram)
//   - s3_bytes_transferred_total{direction}      (counter)
//
// Without this option no collectors are created (zero Prometheus
// footprint).
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) { o.metrics = newMetricsCollector(reg) }
}

// PutOption tunes a single [Client.Put] call.
type PutOption func(*putConfig)

type putConfig struct {
	contentType     string
	cacheControl    string
	contentEncoding string
	metadata        map[string]string
}

// WithPutContentType sets the Content-Type header on the uploaded
// object. The S3 SDK does NOT auto-detect MIME; pass it explicitly
// for object metadata to be correct.
func WithPutContentType(t string) PutOption {
	return func(c *putConfig) { c.contentType = t }
}

// WithPutCacheControl sets Cache-Control on the uploaded object —
// honoured by browsers when fetching the object via the presigned
// URL or public bucket.
func WithPutCacheControl(v string) PutOption {
	return func(c *putConfig) { c.cacheControl = v }
}

// WithPutContentEncoding sets Content-Encoding (e.g. "gzip"). The
// caller is responsible for encoding the body to match.
func WithPutContentEncoding(v string) PutOption {
	return func(c *putConfig) { c.contentEncoding = v }
}

// WithPutMetadata adds user-defined metadata. Stored as
// `x-amz-meta-<key>` headers on the object; merged into any
// existing metadata on overwrite.
func WithPutMetadata(m map[string]string) PutOption {
	return func(c *putConfig) {
		if c.metadata == nil {
			c.metadata = map[string]string{}
		}
		for k, v := range m {
			c.metadata[k] = v
		}
	}
}
