// Package redisclient is the kit's thin wrapper around
// github.com/redis/go-redis/v9. It provides:
//
//   - URL-driven Connect with exponential-backoff retry on the
//     initial PING (matches db.Connect / natsclient.Connect).
//   - Slog + Prometheus observability via Options (opt-in, zero
//     overhead when omitted).
//   - An escape hatch via Client.Redis() so callers can use any
//     go-redis API the wrapper doesn't expose.
//
// The package name `redisclient` is deliberate: the upstream import
// is `redis`, so the kit wrapper picks a non-colliding identifier
// (same convention as clients/nats → `natsclient`).
//
// Typical wiring:
//
//	cli, err := redisclient.Connect(ctx, redisclient.Config{
//	    URL:              "redis://localhost:6379",
//	    ConnectMaxRetries: 5,
//	}, redisclient.WithLogger(logger), redisclient.WithMetrics(reg))
//	if err != nil { return err }
//	defer cli.Close()
//	rdb := cli.Redis()                 // *redis.Client
//
// service.New auto-wires this when service.Config.Redis.URL is set,
// exposing the result as svc.Redis.
package redisclient
