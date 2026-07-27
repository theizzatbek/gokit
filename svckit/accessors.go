package svckit

import (
	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/db"
)

// DB, Auth, and Hasher are built by the core itself — not by a mod —
// so their Must*/Optional* pair lives here rather than alongside any
// particular mod. Optional subsystems get the analogous pair from
// their own mod (e.g. s3mod.Client / s3mod.Optional).
//
//   - MustX panics with a guiding message naming the Config knob that
//     would have wired it. Use in code paths that hard-require the
//     subsystem — failing early at the access site beats a nil-deref
//     deep in a request handler.
//   - OptionalX returns (subsystem, ok) where ok is false when the
//     field is nil. Use in code paths that branch on presence.
//
// The accessors are sugar over s.DB / s.Auth / s.Hasher; the fields
// themselves stay exported so direct field access still compiles.

// MustDB returns s.DB or panics when no database was configured.
func (s *Service[T, C]) MustDB() *db.DB {
	if s.DB == nil {
		panic("svckit: MustDB called but no DB configured (set Config.DB.User)")
	}
	return s.DB
}

// OptionalDB returns (s.DB, true) when a database is configured,
// (nil, false) otherwise.
func (s *Service[T, C]) OptionalDB() (*db.DB, bool) { return s.DB, s.DB != nil }

// MustAuth returns s.Auth or panics when Auth was not configured.
func (s *Service[T, C]) MustAuth() *auth.Auth[C] {
	if s.Auth == nil {
		panic("svckit: MustAuth called but Auth not configured (set Config.Auth.PrivateKeyPEM)")
	}
	return s.Auth
}

// OptionalAuth returns (s.Auth, true) when Auth is configured.
func (s *Service[T, C]) OptionalAuth() (*auth.Auth[C], bool) { return s.Auth, s.Auth != nil }

// MustHasher returns s.Hasher or panics when Auth (which owns the
// Hasher) was not configured.
func (s *Service[T, C]) MustHasher() *auth.Hasher {
	if s.Hasher == nil {
		panic("svckit: MustHasher called but Auth not configured (Hasher is wired alongside Auth — set Config.Auth.PrivateKeyPEM)")
	}
	return s.Hasher
}

// OptionalHasher returns (s.Hasher, true) when Auth is configured.
func (s *Service[T, C]) OptionalHasher() (*auth.Hasher, bool) { return s.Hasher, s.Hasher != nil }
