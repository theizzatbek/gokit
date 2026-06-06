package service

import (
	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/clients/apimap"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/clients/ratelimit"
	redisclient "github.com/theizzatbek/gokit/clients/redis"
	s3client "github.com/theizzatbek/gokit/clients/s3"
	"github.com/theizzatbek/gokit/clients/webhooks"
	"github.com/theizzatbek/gokit/cronmap"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/outbox"
)

// Service exposes every optional subsystem as a public field, but a
// field is nil when the subsystem was not opted into via Config /
// options. Each subsystem has a typed accessor pair:
//
//   - MustX panics with a guiding message that names the Config knob
//     or option which would have wired it. Use in code paths that
//     hard-require the subsystem — failing early at the access site
//     beats a nil-deref deep in a request handler.
//   - OptionalX returns (subsystem, ok) where ok is false when the
//     field is nil. Use in code paths that branch on whether a
//     subsystem is present.
//
// The accessors are sugar over `s.DB`, `s.Auth`, etc.; the fields
// themselves stay exported so existing call sites compile unchanged.

// MustDB returns s.DB or panics when no database was configured.
func (s *Service[T, C]) MustDB() *db.DB {
	if s.DB == nil {
		panic("service: MustDB called but no DB configured (set Config.DB.User)")
	}
	return s.DB
}

// OptionalDB returns (s.DB, true) when a database is configured,
// (nil, false) otherwise.
func (s *Service[T, C]) OptionalDB() (*db.DB, bool) { return s.DB, s.DB != nil }

// MustAuth returns s.Auth or panics when Auth was not configured.
func (s *Service[T, C]) MustAuth() *auth.Auth[C] {
	if s.Auth == nil {
		panic("service: MustAuth called but Auth not configured (set Config.Auth.PrivateKeyPEM)")
	}
	return s.Auth
}

// OptionalAuth returns (s.Auth, true) when Auth is configured.
func (s *Service[T, C]) OptionalAuth() (*auth.Auth[C], bool) { return s.Auth, s.Auth != nil }

// MustNATS returns s.NATS or panics when no NATS client was configured.
func (s *Service[T, C]) MustNATS() *natsclient.Client {
	if s.NATS == nil {
		panic("service: MustNATS called but NATS not configured (set Config.NATS.URL)")
	}
	return s.NATS
}

// OptionalNATS returns (s.NATS, true) when NATS is configured.
func (s *Service[T, C]) OptionalNATS() (*natsclient.Client, bool) { return s.NATS, s.NATS != nil }

// MustRedis returns s.Redis or panics when no Redis client was configured.
func (s *Service[T, C]) MustRedis() *redisclient.Client {
	if s.Redis == nil {
		panic("service: MustRedis called but Redis not configured (set Config.Redis.URL)")
	}
	return s.Redis
}

// OptionalRedis returns (s.Redis, true) when Redis is configured.
func (s *Service[T, C]) OptionalRedis() (*redisclient.Client, bool) {
	return s.Redis, s.Redis != nil
}

// MustNATSMap returns s.NATSMap or panics when no NATSMap runtime
// was configured.
func (s *Service[T, C]) MustNATSMap() *natsmap.Runtime {
	if s.NATSMap == nil {
		panic("service: MustNATSMap called but NATSMap not configured (set Config.NATSMap.*Path)")
	}
	return s.NATSMap
}

// OptionalNATSMap returns (s.NATSMap, true) when NATSMap is configured.
func (s *Service[T, C]) OptionalNATSMap() (*natsmap.Runtime, bool) {
	return s.NATSMap, s.NATSMap != nil
}

// MustAPIMap returns s.APIMap or panics when no APIMap client was
// configured.
func (s *Service[T, C]) MustAPIMap() *apimap.Client {
	if s.APIMap == nil {
		panic("service: MustAPIMap called but APIMap not configured (set Config.APIMap.Path)")
	}
	return s.APIMap
}

// OptionalAPIMap returns (s.APIMap, true) when APIMap is configured.
func (s *Service[T, C]) OptionalAPIMap() (*apimap.Client, bool) {
	return s.APIMap, s.APIMap != nil
}

// MustHasher returns s.Hasher or panics when Auth (which owns the
// Hasher) was not configured.
func (s *Service[T, C]) MustHasher() *auth.Hasher {
	if s.Hasher == nil {
		panic("service: MustHasher called but Auth not configured (Hasher is wired alongside Auth — set Config.Auth.PrivateKeyPEM)")
	}
	return s.Hasher
}

// OptionalHasher returns (s.Hasher, true) when Auth is configured.
func (s *Service[T, C]) OptionalHasher() (*auth.Hasher, bool) {
	return s.Hasher, s.Hasher != nil
}

// MustOutbox returns s.Outbox or panics when the outbox worker was
// not configured (requires WithOutbox + DB + NATSMap).
func (s *Service[T, C]) MustOutbox() *outbox.Worker {
	if s.Outbox == nil {
		panic("service: MustOutbox called but Outbox not configured (pass service.WithOutbox alongside Config.DB.User + Config.NATSMap.*Path)")
	}
	return s.Outbox
}

// OptionalOutbox returns (s.Outbox, true) when the outbox worker is
// configured.
func (s *Service[T, C]) OptionalOutbox() (*outbox.Worker, bool) {
	return s.Outbox, s.Outbox != nil
}

// MustS3 returns s.S3 or panics when no S3 client was configured.
func (s *Service[T, C]) MustS3() *s3client.Client {
	if s.S3 == nil {
		panic("service: MustS3 called but S3 not configured (set Config.S3.Bucket)")
	}
	return s.S3
}

// OptionalS3 returns (s.S3, true) when S3 is configured.
func (s *Service[T, C]) OptionalS3() (*s3client.Client, bool) { return s.S3, s.S3 != nil }

// MustCronMap returns s.CronMap or panics when CronMap was not
// configured (pass WithCronMap or set Config.CronMap.Path).
func (s *Service[T, C]) MustCronMap() *cronmap.Runtime {
	if s.CronMap == nil {
		panic("service: MustCronMap called but CronMap not configured (pass service.WithCronMap or set Config.CronMap.Path)")
	}
	return s.CronMap
}

// OptionalCronMap returns (s.CronMap, true) when CronMap is configured.
func (s *Service[T, C]) OptionalCronMap() (*cronmap.Runtime, bool) {
	return s.CronMap, s.CronMap != nil
}

// MustRateLimiter returns s.RateLimiter or panics when the rate
// limiter was not configured (requires WithRateLimit + Redis).
func (s *Service[T, C]) MustRateLimiter() *ratelimit.Redis {
	if s.RateLimiter == nil {
		panic("service: MustRateLimiter called but RateLimiter not configured (pass service.WithRateLimit alongside Config.Redis.URL)")
	}
	return s.RateLimiter
}

// OptionalRateLimiter returns (s.RateLimiter, true) when the rate
// limiter is configured.
func (s *Service[T, C]) OptionalRateLimiter() (*ratelimit.Redis, bool) {
	return s.RateLimiter, s.RateLimiter != nil
}

// MustWebhooksWorker returns s.WebhooksWorker or panics when the
// webhooks worker was not configured.
func (s *Service[T, C]) MustWebhooksWorker() *webhooks.Worker {
	if s.WebhooksWorker == nil {
		panic("service: MustWebhooksWorker called but worker not configured (pass service.WithWebhooks with StartWorker=true)")
	}
	return s.WebhooksWorker
}

// OptionalWebhooksWorker returns (s.WebhooksWorker, true) when the
// worker is configured.
func (s *Service[T, C]) OptionalWebhooksWorker() (*webhooks.Worker, bool) {
	return s.WebhooksWorker, s.WebhooksWorker != nil
}

// MustWebhooksFanout returns s.WebhooksFanout or panics when the
// webhooks fanout was not configured.
func (s *Service[T, C]) MustWebhooksFanout() *webhooks.Fanout {
	if s.WebhooksFanout == nil {
		panic("service: MustWebhooksFanout called but fanout not configured (pass service.WithWebhooks with StartFanout=true)")
	}
	return s.WebhooksFanout
}

// OptionalWebhooksFanout returns (s.WebhooksFanout, true) when the
// fanout is configured.
func (s *Service[T, C]) OptionalWebhooksFanout() (*webhooks.Fanout, bool) {
	return s.WebhooksFanout, s.WebhooksFanout != nil
}
