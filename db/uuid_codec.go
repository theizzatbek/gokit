package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// registerUUIDByteArrayCodec teaches pgx that a Go `[16]byte` value
// should encode to (and decode from) Postgres's uuid type
// (OID 2950). Without this, callers must wrap raw byte arrays in
// pgtype.UUID before passing them as query args:
//
//	_, err = d.Exec(ctx, "INSERT ... VALUES ($1)",
//	    pgtype.UUID{Bytes: b, Valid: true})
//
// With the hook installed at AfterConnect time, the raw [16]byte
// flows through unchanged:
//
//	_, err = d.Exec(ctx, "INSERT ... VALUES ($1)", b)
//
// Scanning a uuid column into a [16]byte destination works
// symmetrically.
//
// pgx's pgtype.UUIDCodec handles [16]byte natively in both
// directions; the pre-v1.0.1 hole was that pgx's per-connection
// TypeMap had no default-pg-type registration for [16]byte, so
// type inference at query time returned "unable to encode 0x..
// into binary format for uuid (OID 2950): cannot find encode
// plan". RegisterDefaultPgType closes that gap on every fresh
// connection.
//
// Caller-supplied WithConnInit hooks can override the mapping
// (or register additional Go-type → pg-type defaults) on the
// same TypeMap, in their own AfterConnect slot — the kit-internal
// hook always runs first, so user overrides take effect on top.
func registerUUIDByteArrayCodec(ctx context.Context, conn *pgx.Conn) error {
	conn.TypeMap().RegisterDefaultPgType([16]byte{}, "uuid")
	return nil
}
