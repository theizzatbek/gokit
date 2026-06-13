// Package ids is the kit's standard prefixed-ULID utility.
//
// Services on gokit conventionally tag identifiers with a short
// type-prefix: `user_01H…`, `acc_01H…`, `prod_01H…`. The pattern is
// everywhere but was unstandardised — every consumer rewrote the
// same `NewID(prefix)` / `ParseID(prefix, s)` / `FormatID(prefix,
// raw)` helpers on top of [github.com/oklog/ulid/v2]. This package
// is the single canonical implementation.
//
// # Wire shape
//
// Every ID is `<prefix><26-char Crockford-Base32 ULID>`. Crockford
// Base32 is the ULID-spec encoding — case-insensitive on Parse, but
// [New] / [Format] always emit uppercase for stability.
//
// The 26-char suffix decodes to exactly 16 raw bytes (the binary
// ULID layout: 6 bytes ms-precision timestamp + 10 bytes randomness).
// Same 16 bytes pgx writes to a Postgres `uuid` column via the
// [16]byte codec the kit registers in db.Connect (see
// `db/uuid_codec.go`, shipped in v1.0.1).
//
// Functions
//
//   - [New]     — mint a new prefixed ID. Time-sortable. Monotonic
//     within a process under contention.
//   - [Parse]   — validate + strip prefix, return raw 16 bytes for
//     storage as `uuid`.
//   - [Format]  — inverse of Parse for callers holding raw bytes
//     (typical: row scanned from a `uuid` column).
//   - [RegisterValidator] — wire a `validate:"prefix=prod_"` struct
//     tag for declarative DTO validation.
//
// # Error codes
//
// All parse-stage errors are [*errs.Error] of Kind = Validation:
//
//   - [CodeBadPrefix] — input doesn't start with the expected prefix
//     (including length mismatch or empty input).
//   - [CodeBadSuffix] — 26-char suffix isn't a valid Crockford-Base32
//     ULID (wrong length, illegal character, etc).
//
// `errors.Is` works against the package-level sentinels [ErrBadPrefix]
// and [ErrBadSuffix] for branching code, but most callers should
// match on `e.Code` for stable wire / log behaviour.
//
// # Goroutine safety
//
// [New] is goroutine-safe: it serialises around a package-level
// monotonic entropy source so two concurrent calls in the same
// millisecond still produce strictly increasing ULIDs (per the ULID
// spec). [Parse] / [Format] are pure functions over their inputs.
package ids
