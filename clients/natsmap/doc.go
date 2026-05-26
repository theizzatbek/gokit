// Package natsmap is the declarative YAML layer for NATS subscribers and
// publishers, symmetric to clients/apimap. Subscribers and publishers
// are described in YAML (subscribers.yaml, publishers.yaml, or a
// combined file); Go code registers typed handlers and publishers by
// name; Build returns a goroutine-safe *Runtime exposing Drain() and
// Publish[T](...).
//
// Lifecycle:
//
//	New → LoadFile (n) → RegisterHandler/Publisher (n) → Build → Runtime → Drain
//
// Errors are typed *errs.Error with stable Code* constants
// (natsmap_*). Build aggregates validation failures via errors.Join.
package natsmap
