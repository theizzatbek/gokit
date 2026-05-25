// Package gokit is the umbrella for the Go service kit. Each subpackage
// is independently importable. Subpackages:
//
//   - fibermap:          YAML-declarative Fiber router
//   - errs:              typed domain errors + HTTP mapping
//   - db:                pgx pool + transactions + healthcheck
//   - db/sqb:            opt-in squirrel wrapper over db
//   - auth:              JWT + refresh + ready-to-mount middleware
//   - clients/httpc:     outbound *http.Client with retry/timeout
//   - clients/apimap:    declarative outbound HTTP via YAML
//   - clients/nats:      typed JetStream wrapper (package natsclient)
//
// The root gokit package itself has no exported symbols. Importing one
// subpackage does not pull the others.
//
// See README.md for the per-package overview and dependency rules.
package gokit
