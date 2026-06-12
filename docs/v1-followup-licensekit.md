# v1 followup — LicenseKit integrator friction report

> **Received:** 2026-06-12
> **Source:** Friction encountered wiring
> [`github.com/LicenseKit/backend`](https://github.com/LicenseKit/backend)
> on top of gokit `v1.0.0`. Subsystems exercised: `fibermap`, `errs`,
> `errsval`, `db`, `db/sqb`, `db/migrate`, `db/testdb`, `auth`,
> `auth/apikeypg`, `auth/fibermount`, `clients/cache`,
> `clients/ratelimit`, `clients/redis`.
> **Status:** Triaged + scheduled. See classification + sequencing
> below. Per-item progress tracked via the existing `Acceptance` checkbox
> lists in [§ Original proposal](#original-proposal).

This file is a frozen record of the first-integrator friction report.
The kit-side response is to ship most items as **`v1.0.1` patches and
`v1.1.0` minor** — not v2. Items that legitimately are breaking move to
[`docs/v2-backlog.md`](v2-backlog.md) with an explicit cross-ref.

When all `Acceptance` boxes on an item below are ticked, strike the item
header (`~~P0-1...~~`) and add a one-line *"shipped in `vX.Y.Z` (commit
SHA)"* note next to it. When every item is shipped, this file is
archived (move to `docs/CHANGELOG-followup-archive.md` or just
`git rm`).

---

## Semver classification

| # | Item | Target | Notes |
|---|---|---|---|
| P0-1 | `fibermap/bind` discards `validator.ValidationErrors` type | **`v1.0.1` PATCH** | bug fix; `errors.Join` swap, no API change |
| P0-2 | `service.buildAuth` doesn't thread `APIKeyHashSecret` | **`v1.1.0` MINOR** | additive: env var, `AuthConfig` field, `auth.WithAPIKeyHashSecret` Option |
| P0-3 | `fibermap.ErrorHandler` install coupled to `WithBodyLimit` | **`v1.0.1` PATCH** | internal rewire (optional add of `service.WithErrorHandler` is MINOR — track as P2-16) |
| P1-4 | `fibermap.ErrsvalBindError` helper | **`v1.1.0` MINOR** | new exported func; depends on P0-1 for full `Details[]` extraction |
| P1-5 | public `gokit/crypto` package | **`v1.1.0` MINOR** | new package (lift + harden existing `clients/webhooks/storepg/crypto.go`) |
| P1-6 | public `gokit/ids` package | **`v1.1.0` MINOR** | new package (prefixed ULIDs + validator tag) |
| P1-7 | combined `RegisterHandlerWithInput[T, In]` | **ALREADY IN `v1.0.0`** — doc lift only | `fibermap.RegisterHandlerWithInput[T, Input any]` exists in [`fibermap/bind_input.go`](../fibermap/bind_input.go) since v1.0.0 with the exact contract LicenseKit proposes (struct with `Body`/`Params`/`Query`/`Headers` field names, multi-source bind, auto-attaches OpenAPI options). LicenseKit's report missed it. **Action:** D-3 (README quickstart) should demonstrate `RegisterHandlerWithInput` next to the single-source variants. Crosslink in [`docs/v2-backlog.md`](v2-backlog.md) § "RegisterHandlerWith* family" closes the pending decision: keep both (single-source for ergonomics + multi-source for combined cases), revisit deprecation of single-source variants only if `WithInput` proves enough.|
| P1-8 | pgx `[16]byte` ↔ uuid auto-codec | **`v1.0.1` PATCH** (auto-codec path) OR **`v1.1.0` MINOR** (`db.UUIDArg` helper path) | pick one — auto-codec is silent ergonomic win, helper is more explicit |
| P1-9 | `auth.WithAPIKeyHashSecret([]byte)` Option | **`v1.1.0` MINOR** | bundled with P0-2 in one PR |
| P1-10 | `auth.GenerateAPIKey(pepper)` recipe helper | **`v1.1.0` MINOR** | new exported func |
| P2-11 | `CORS_ORIGINS` env-driven | **`v1.1.0` MINOR** | additive `ServiceConfig` field + auto-apply in `service.New` |
| P2-12 | `service.WithExtraValidators(map[string]validator.Func)` | **`v1.1.0` MINOR** | new Option |
| P2-13 | Sentry / OTel env auto-enable | **`v1.0.1` PATCH** (no new exports — pure internal default) OR **`v1.1.0` MINOR** if surface a `WithoutAutoSentry` / `WithoutAutoOTel` escape hatch | likely PATCH; honour `OTEL_SDK_DISABLED` |
| P2-14 | `service.Boot` / `BootSeed` | **`v1.1.0` MINOR** | new top-level constructor |
| P2-15 | `audit` ↔ `fibermap` adapter | **`v1.1.0` MINOR** | new `HandlerOption` (or new subpackage `audit/fibermap/`) |
| P2-16 | `service.WithErrorHandler(fiber.ErrorHandler)` | **`v1.1.0` MINOR** | new Option (the clarifying half of P0-3) |
| D-1 | Production deployment checklist | **`v1.0.1` PATCH** | doc-only; in `service/README.md` |
| D-2 | "Common gotchas" page | **`v1.0.1` PATCH** | doc-only; in `docs/` |
| D-3 | README quickstart typed-binding refresh | **`v1.0.1` PATCH** | doc-only; promote `RegisterHandlerWith{Body,Input}` over raw `fiber.Ctx` |

---

## Sequencing

### `v1.0.1` patch wave

Pure bug fixes + docs; no new exports. Target ship: end of June 2026
(within ~1-2 weeks of receiving this report — these bite **every**
first integrator).

1. **P0-1** — `fibermap/bind` validator-type preservation (`errors.Join`).
2. **P0-3** — `ErrorHandler` / `BodyLimit` decouple (`service/run.go`).
3. **P1-8** — pgx `[16]byte` ↔ uuid auto-codec (`db/db.go`
   `AfterConnect`). If auto-codec is rejected on grounds of
   "implicit conversion magic", do the explicit `db.UUIDArg`
   helper as `v1.1.0` MINOR instead.
4. **P2-13** — Sentry / OTel env auto-enable (PATCH path: no new
   surface, just default behaviour).
5. **D-1, D-2, D-3** — docs.

After ship → `git tag v1.0.1` + roll `[Unreleased]` → `[v1.0.1] -
YYYY-MM-DD` in `CHANGELOG.md`. Race + integration matrices on PR-gate
remain unchanged.

### `v1.1.0` minor wave

Additive helpers + new packages. Target ship: mid-July 2026.

6. **P0-2 + P1-9** — `auth.APIKeyHashSecret` end-to-end (env var,
   `AuthConfig` field, `auth.WithAPIKeyHashSecret` Option). One PR.
7. **P1-4** — `fibermap.ErrsvalBindError` recommended-default helper.
   Depends on P0-1 already in `v1.0.1`.
8. **P1-5** — `gokit/crypto` public package (lift + tighten).
9. **P1-6** — `gokit/ids` public package (prefixed ULIDs).
10. **P1-7** — **NO CODE NEEDED.** `RegisterHandlerWithInput[T, Input
    any]` is already shipped in v1.0.0 with the exact contract
    LicenseKit's proposal describes (`Body`/`Params`/`Query`/`Headers`
    field-name convention, multi-source bind, auto-OpenAPI). The
    integrator's report missed the existing helper. The fix is D-3
    (README quickstart promotes it next to single-source variants).
    No deprecation of single-source helpers — they remain the
    ergonomic single-binding shortcut.
11. **P1-10** — `auth.GenerateAPIKey` recipe.
12. **P2-11** — `CORS_ORIGINS` env-driven.
13. **P2-12** — `service.WithExtraValidators`.
14. **P2-14** — `service.Boot` / `BootSeed`.
15. **P2-15** — `audit` fibermap adapter.
16. **P2-16** — `service.WithErrorHandler` opt-in.

After ship → `git tag v1.1.0` + CHANGELOG roll.

### v2 — crosslinks only

Nothing in this proposal is freshly added to the v2 backlog. The single
crosslink: P1-7 sets up the legacy `RegisterHandlerWith*` removal,
which lives in [`docs/v2-backlog.md`](v2-backlog.md) § "fibermap.RegisterHandlerWith*
family". That section is updated in the same PR as this file to record
the decision path.

---

## Original proposal

Preserved verbatim. The triage table above is the kit-maintainer's
overlay; the structure below is the integrator's analysis.

---

> Audience: a developer (or AI agent) working on
> [`github.com/theizzatbek/gokit`](https://github.com/theizzatbek/gokit).
>
> Source of these items: real friction encountered while wiring
> [`github.com/LicenseKit/backend`](https://github.com/LicenseKit/backend)
> on top of gokit v1.0.0 — a service that uses fibermap, errs, errsval,
> db, db/sqb, db/migrate, db/testdb, auth, auth/apikeypg, auth/fibermount,
> clients/cache, clients/ratelimit, and clients/redis.
>
> Each item below should land as a small, focused PR. They are listed
> in priority order. P0 fixes are correctness/security bugs that bite
> the FIRST integrator; P1 items remove load-bearing workarounds that
> every consumer rewrites; P2 items are quality-of-life.

---

## How to use this document

For each item:

1. Read the **Problem** section to understand the user-visible symptom.
2. Look at the **Repro** snippet — a minimal example showing the bug
   today.
3. Apply the **Proposed change** as written. Code sketches are
   intentionally complete enough to drop in; if something doesn't
   compile, it's a typo in the spec, not a design intent.
4. Add the listed **Tests**. The existing test conventions in the
   touched file are the baseline.
5. Check the **Acceptance** box list before opening the PR.

Reasoning notes are in `> blockquote` callouts — keep them or strip
them, your call.

---

# P0 — Correctness bugs

These are wrong-by-default behaviours that surface as runtime failures.

## P0-1. `fibermap/bind` discards `validator.ValidationErrors` type

**Affected files:** `fibermap/bind/bind.go` (Body / Query / Params /
Header funcs).

**Problem.** The wrap is `fmt.Errorf("%w: %v", ErrValidateBody, err)`.
`%w` wraps the sentinel; `%v` stringifies the actual
`validator.ValidationErrors`. Downstream `errors.As(err, &vErrs)`
fails — the chain only contains the sentinel.

This breaks `errs/errsval.FromValidator`: it can't recover the
per-field error list, and the entire point of `errsval` (producing
`*errs.Error{Kind: Validation, Details: []FieldError}`) is unreachable
from a bind-wrapped error.

**Repro.**

```go
type Req struct{ Email string `validate:"required,email"` }
// In a handler registered via RegisterHandlerWithBody:
//   user POSTs {} → handler never runs
//   default bind error: {"error":"bind: validate body: ..."}
//   even a custom SetBindErrorHandler can't extract per-field
//   detail because errsval.FromValidator returns the err unchanged.
```

**Proposed change.** Use `errors.Join` (Go 1.20+) so the
`validator.ValidationErrors` stays addressable via `errors.As`:

```go
// fibermap/bind/bind.go
func Body[T any](c BodyParser, v Validator) (T, error) {
    var body T
    if err := c.BodyParser(&body); err != nil {
        return body, errors.Join(ErrParseBody, err)
    }
    if v != nil {
        if err := v.Struct(&body); err != nil {
            return body, errors.Join(ErrValidateBody, err)
        }
    }
    return body, nil
}
```

Same change for `Query`, `Params`, `Header`.

**Tests.**

```go
func TestBody_ValidationErrorPreservesType(t *testing.T) {
    // … set up bind.Body with a struct that fails one validator …
    var vErrs validator.ValidationErrors
    if !errors.As(err, &vErrs) {
        t.Fatal("validator.ValidationErrors must be addressable via errors.As")
    }
    if len(vErrs) == 0 {
        t.Fatal("expected at least one field error")
    }
}
```

Also: extend `errs/errsval/errsval_test.go` with a case that goes
through `bind.Body` → `errsval.FromValidator` and asserts non-empty
`Details`.

**Acceptance.**

- [ ] `errors.Is(err, ErrValidateBody)` still works (no regression on
      sentinel detection).
- [ ] `errors.As(err, &vErrs)` succeeds when validator failed.
- [ ] `errsval.FromValidator(bindErr).(*errs.Error).Details` is
      non-empty for failing fields.
- [ ] `defaultBindError` and any user-registered bind-error handler
      can produce per-field JSON output.

---

## P0-2. `service.buildAuth` doesn't pass `APIKeyHashSecret` to `auth.New`

**Affected files:** `service/build.go::buildAuth`, `service/config.go`
(`AuthConfig`), maybe a new `auth/options.go` option.

**Problem.** `auth.Auth[C].APIKey(store, opts...)` **panics** with
`api_key_missing_secret` on the first request if
`apiKeyHashSecret` is empty. But `service.AuthConfig` doesn't expose
the field and `service.buildAuth` doesn't pass it:

```go
// service/build.go (today)
a, err := auth.New[C](auth.Config{
    Issuer:     s.cfg.Auth.Issuer,
    Keys:       keySet,
    AccessTTL:  s.cfg.Auth.AccessTTL,
    RefreshTTL: s.cfg.Auth.RefreshTTL,
    // ← APIKeyHashSecret missing
})
```

This forces every service that wants API-key auth to either bypass
`service.WithAuth` entirely (building a parallel `auth.Auth[C]`) or
accept the panic.

LicenseKit currently builds its own `auth.Auth[struct{}]` next to
`svc.Auth` — wasted ceremony.

**Proposed change.**

1. Add field to `service.AuthConfig`:

```go
// service/config.go
type AuthConfig struct {
    PrivateKeyPEM string `env:"PRIVATE_KEY_PEM"`
    KID           string `env:"KID"         envDefault:"k1"`
    Issuer        string `env:"ISSUER"      envDefault:"gokit"`
    AccessTTL     time.Duration `env:"ACCESS_TTL"  envDefault:"15m"`
    RefreshTTL    time.Duration `env:"REFRESH_TTL" envDefault:"720h"`

    // APIKeyHashSecret is the HMAC pepper auth.APIKey middleware
    // uses to derive the hash before calling KeyStore.Lookup. 32
    // raw bytes (base64-encoded). Required when api_key middleware
    // is wired; safe to omit for pure-JWT services.
    APIKeyHashSecret string `env:"APIKEY_HASH_SECRET"`
}
```

2. Decode it in `buildAuth`:

```go
var pepper []byte
if s.cfg.Auth.APIKeyHashSecret != "" {
    pepper, err = decodeBase64(s.cfg.Auth.APIKeyHashSecret)
    if err != nil {
        return xerrs.Wrap(err, xerrs.KindValidation,
            CodeAuthInvalidAPIKeyHashSecret,
            "service: AUTH_APIKEY_HASH_SECRET invalid base64")
    }
}
a, err := auth.New[C](auth.Config{
    // … existing fields …
    APIKeyHashSecret: pepper,
}, /* existing opts */)
```

3. Add an `auth.Option`-form too for callers who construct `Auth` by
   hand:

```go
// auth/options.go
func WithAPIKeyHashSecret(secret []byte) Option {
    return func(o *options) { o.apiKeyHashSecretOverride = secret }
}
```

And `auth.New` should accept the override after applying Config:

```go
if len(o.apiKeyHashSecretOverride) > 0 {
    a.apiKeyHashSecret = o.apiKeyHashSecretOverride
}
```

**Tests.**

- `service/build_test.go`: when `AUTH_APIKEY_HASH_SECRET` env set,
  `svc.Auth.APIKey(store)` constructs without panic.
- `auth/apikey_test.go`: a Config without APIKeyHashSecret + an
  Option `WithAPIKeyHashSecret([]byte("…"))` produces a working
  middleware.
- Length check: secret < 32 bytes yields a validation error at
  `service.New` time, not a runtime middleware panic.

**Acceptance.**

- [ ] `service.New + WithAuth + fibermount.MountAPIKeyFactory` works
      with NO manual `auth.New` call in user code.
- [ ] LicenseKit can drop its standalone `auth.Auth[struct{}]`
      construction.

---

## P0-3. `fibermap.ErrorHandler` install coupled to `WithBodyLimit`

**Affected file:** `service/run.go` (~line 117).

**Problem.** The fiber.Config bundle that installs
`fibermap.ErrorHandler(s.logger)` is gated by `if s.opts.bodyLimit > 0`.
If the operator doesn't explicitly call `service.WithBodyLimit`,
fiber's default error handler runs and typed `*errs.Error` returns
HTTP 500 with `.Error()` plain-text string.

This is mentioned in passing in `service/README.md`'s `WithBodyLimit`
description but is a non-obvious tie. Every first-time integrator
either skips body-limit and hits this, or scrolls the README and
discovers the dependency by accident.

**Proposed change.** Decouple. Always install the ErrorHandler;
only bundle BodyLimit when explicitly set.

```go
// service/run.go (current)
if s.opts.bodyLimit > 0 {
    out = append(out, fibermap.WithFiberConfig(fiber.Config{
        BodyLimit:    s.opts.bodyLimit,
        ErrorHandler: fibermap.ErrorHandler(s.logger),
    }))
}

// proposed
fiberCfg := fiber.Config{ErrorHandler: fibermap.ErrorHandler(s.logger)}
if s.opts.bodyLimit > 0 {
    fiberCfg.BodyLimit = s.opts.bodyLimit
}
out = append(out, fibermap.WithFiberConfig(fiberCfg))
```

Or, even cleaner, add a dedicated option for clarity:

```go
// Useful when the caller wants to override ErrorHandler with
// e.g. sentrykit.WrapErrorHandler.
func WithErrorHandler(h fiber.ErrorHandler) Option { ... }
```

**Tests.** Single fiber.App `Test()` round-trip with a handler that
returns `errs.NotFound("xyz", "msg")`. Without `WithBodyLimit`,
assert status=404 and body contains `"code":"xyz"`.

**Acceptance.**

- [ ] `service.New(ctx, cfg)` with NO `WithBodyLimit` and NO
      `WithRouter*` produces typed `{code, message}` JSON for
      errors returned from handlers.
- [ ] `service/README.md` no longer mentions the coupling in the
      `WithBodyLimit` description.

---

# P1 — Ergonomics blockers (every service rewrites these)

## P1-4. No errsval-aware default for `Engine.SetBindErrorHandler`

**Affected file:** `fibermap/engine.go` (`defaultBindError`),
possibly a new `fibermap/bind_error_errsval.go`.

**Problem.** The default emits `{"error":"<full message>"}`. The
kit's own wire convention (everywhere else!) is `{code, message,
details[]}`. Operators end up writing ~50 lines of glue to bridge
the two shapes — including:
- Calling `errsval.FromValidator(err)` to recover `Details[]`
  (assumes P0-1 is fixed)
- Mapping `bind.ErrValidateBody/Query/Params/Header` to source-
  aware codes
- Falling through to `errs.Validation("invalid_<source>", msg)`
  for parse-stage errors
- Emitting via `errs.HTTP(e) → c.Status(s).JSON(body)`

**Proposed change.** Ship the canonical implementation as
`fibermap.ErrsvalBindError[T any]`:

```go
// fibermap/bind_error_errsval.go (new)

// ErrsvalBindError is the recommended SetBindErrorHandler value for
// services using errs as their error contract. It maps bind failures
// to {code, message, details[]} matching the rest of the kit's wire
// shape.
//
//   eng.SetBindErrorHandler(fibermap.ErrsvalBindError[AppCtx])
//
// Codes:
//   parse/validate body   → "invalid_body"
//   parse/validate query  → "invalid_query"
//   parse/validate params → "invalid_params"
//   parse/validate header → "invalid_header"
//   (other)               → "invalid_request"
func ErrsvalBindError[T any](c *Context[T], err error) error {
    if err == nil { return nil }
    if conv := errsval.FromValidator(err); conv != err {
        if e, ok := errs.AsType[*errs.Error](conv); ok {
            e.Code = bindSourceCode(err)
            status, body := errs.HTTP(e)
            return c.Status(status).JSON(body)
        }
    }
    e := errs.Validation(bindSourceCode(err), err.Error())
    status, body := errs.HTTP(e)
    return c.Status(status).JSON(body)
}

func bindSourceCode(err error) string {
    switch {
    case errors.Is(err, bind.ErrValidateBody),   errors.Is(err, bind.ErrParseBody):   return "invalid_body"
    case errors.Is(err, bind.ErrValidateQuery),  errors.Is(err, bind.ErrParseQuery):  return "invalid_query"
    case errors.Is(err, bind.ErrValidateParams), errors.Is(err, bind.ErrParseParams): return "invalid_params"
    case errors.Is(err, bind.ErrValidateHeader), errors.Is(err, bind.ErrParseHeader): return "invalid_header"
    default: return "invalid_request"
    }
}
```

Then make `defaultBindError` either delegate to `ErrsvalBindError`
(if errsval is acceptable as a fibermap dep — it's just `errs` +
`go-playground/validator/v10` which fibermap already drags in) OR
leave `defaultBindError` as-is and prominently document
`ErrsvalBindError` as the recommended choice in
`fibermap/README.md`.

**Tests.** Round-trip:

```go
// validator failure (after P0-1 is fixed)
resp := app.Test(req)
assert.Status(400)
assert.JSON(`{"code":"invalid_query","message":"…","details":[{"field":"limit","rule":"lte","param":"1000"}]}`)
```

**Acceptance.**

- [ ] `fibermap.ErrsvalBindError` exists, exported, documented.
- [ ] LicenseKit's `internal/httpapi/bind_error.go` becomes a
      one-liner: `eng.SetBindErrorHandler(fibermap.ErrsvalBindError[AppCtx])`.

---

## P1-5. Public `gokit/crypto` package for at-rest AES-GCM

**Affected paths:** new `crypto/` (top-level), maybe deprecate
private `clients/webhooks/storepg/crypto.go`.

**Problem.** Every service that stores secrets in Postgres
(refresh tokens, OAuth tokens, signing keys, webhook secrets,
…) writes the same AES-256-GCM Seal/Open helper. There are
already TWO copies inside gokit:
- `clients/webhooks/storepg/crypto.go` (private)
- `auth/refreshpg` may have its own (check)

External consumers (LicenseKit) make a THIRD. The implementation
isn't hard but it's load-bearing — getting nonce reuse wrong is a
catastrophic bug.

**Proposed change.** Promote the webhooks-storepg helper to a
public package, add Keychain (kid → key) for rotation, add version
byte for ciphersuite migration:

```go
// crypto/masterkey.go
package crypto

const sealVersion byte = 0x01

type MasterKey struct{ aead cipher.AEAD }

func NewMasterKey(key []byte) (*MasterKey, error)              // requires 32 bytes (AES-256)
func NewMasterKeyFromBase64(s string) (*MasterKey, error)     // liberal: std/url, raw/padded
func (mk *MasterKey) Seal(plaintext []byte) ([]byte, error)   // returns version || nonce(12) || ct || tag(16)
func (mk *MasterKey) Open(sealed []byte) ([]byte, error)      // ErrCiphertext on any tamper/wrong-key/truncate

// crypto/keychain.go — kid-byte → MasterKey, sealed blob carries kid in version-byte
type Keychain struct{ /* … */ }
func NewKeychain(active byte, keys map[byte][]byte) (*Keychain, error)
func (kc *Keychain) Seal(plaintext []byte) ([]byte, error)    // version || kid || nonce || ct || tag
func (kc *Keychain) Open(sealed []byte) ([]byte, error)
```

**Tests.** Lift LicenseKit's `internal/crypto/masterkey_test.go`
nearly verbatim — it already covers nonce-randomness, tamper-
detection on every byte position, length validation, base64
flavour acceptance.

**Acceptance.**

- [ ] `webhooks/storepg/crypto.go` rewritten as a thin wrapper or
      deleted entirely.
- [ ] `auth/refreshpg` (or any other at-rest sealing path) uses
      the new package.
- [ ] LicenseKit can replace its `internal/crypto/` package with
      `gokit/crypto`.

---

## P1-6. Public `gokit/ids` — prefixed-ULID utility

**Affected path:** new `ids/`.

**Problem.** API services on gokit produce prefixed IDs:
`user_01H…`, `acc_01H…`, `prod_01H…`. The pattern is everywhere
but unstandardised. Each service builds its own
`NewID(prefix)` / `ParseID(prefix, s)` helper on top of
`oklog/ulid/v2`.

**Proposed change.**

```go
// ids/ids.go
package ids

// New returns "<prefix>" + 26 Crockford-Base32 ULID chars.
// Time-sortable. Monotonic within a process.
func New(prefix string) string

// Parse strips the prefix, validates the ULID-shaped suffix,
// returns the raw 16 bytes. Wrong prefix → *errs.Error{Kind:Validation,
// Code:"id_bad_prefix"}; wrong suffix → code "id_bad_suffix".
func Parse(prefix, s string) ([16]byte, error)

// Format is the inverse of Parse for callers holding raw bytes
// (typical: row scanned from pgx uuid column).
func Format(prefix string, raw [16]byte) string

// RegisterValidator wires a `validate:"prefix=prod_"` tag on the
// supplied *validator.Validate so DTOs can validate inbound IDs
// declaratively.
func RegisterValidator(v *validator.Validate) error
```

**Tests.** LicenseKit's `internal/domain/ids_test.go` is a
ready-made reference; lift the round-trip + bad-prefix + bad-suffix
cases.

**Acceptance.**

- [ ] LicenseKit's `internal/domain/ids.go` becomes a thin
      re-export of `gokit/ids`.

---

## P1-7. `RegisterHandlerWithInput[T, In]` — combined body+params+query

**Affected file:** new `fibermap/bind_register_input.go` (or
extend `bind_register.go`).

**Problem.** `RegisterHandlerWithBody`, `RegisterHandlerWithQuery`,
`RegisterHandlerWithParams`, `RegisterHandlerWithHeaders` are each
single-source. A common shape — body + path-param (e.g. `POST
/products/:id/policies`) — has NO combined helper. Operators are
forced back to manual `c.Ctx.Params("id")` extraction inside a
`WithBody` handler.

**Proposed change.** A single `WithInput` that walks the struct's
embedded fields by source-tag:

```go
type CreatePolicyInput struct {
    Params struct {
        ProductID string `params:"id" validate:"required"`
    }
    Body struct {
        Name        string `json:"name" validate:"required,min=1,max=200"`
        Type        string `json:"type" validate:"required,oneof=perpetual subscription"`
        DefaultEnts map[string]any `json:"default_ents"`
    }
}

fibermap.RegisterHandlerWithInput(eng, "policy.create",
    func(c *fibermap.Context[AppCtx], in CreatePolicyInput) error { … })
```

Implementation strategy: the helper reflects struct fields, calls
`bind.Params` / `bind.Body` / `bind.Query` / `bind.Header` for
each tagged sub-struct, validates the composed result. Single
struct tag mapping:

| sub-struct name | source        | bind func |
|---|---|---|
| `Params` (or any field with `params:""` recurse) | URL params | `bind.Params` |
| `Body`   | JSON body | `bind.Body` |
| `Query`  | query string | `bind.Query` |
| `Header` | request headers | `bind.Header` |

Alternative cheaper design — a flat struct with mixed tags:

```go
type CreatePolicyInput struct {
    ProductID   string `params:"id"   validate:"required"`
    Name        string `json:"name"   validate:"required"`
    DefaultEnts map[string]any `json:"default_ents"`
}
```

Helper calls `c.Ctx.ParamsParser(&in)` THEN `c.Ctx.BodyParser(&in)`
(both honor the right tag — fiber's parsers tolerate unknown
fields). Validator runs once over the whole struct.

The flat-struct variant is simpler; the nested variant is more
explicit but reflects-heavier.

**Acceptance.**

- [ ] LicenseKit's two body+param handlers
      (`CreatePolicy`, `RenewLicense`) drop their inline
      `c.Ctx.Params("id")` calls.

---

## P1-8. pgx `[16]byte` ↔ uuid auto-conversion

**Affected file:** `db/db.go` or `db/conn.go`.

**Problem.** pgx5 returns `failed to encode args[0]: unable to
encode 0x1 into binary format for uuid (OID 2950): cannot find
encode plan` when a query argument is `[16]byte`. Consumers wrap
with `pgtype.UUID{Bytes: b, Valid: true}`. Every repo writes
`uuidArg(b)` / `uuidBytes` helpers.

**Proposed change.** Register a custom encoder in
`db.Connect`:

```go
// db/db.go — inside Connect, after pool acquisition
cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
    // Register [16]byte ↔ uuid (OID 2950) so callers can pass raw
    // ULID bytes / KEY-decryption results without an explicit
    // pgtype.UUID wrap.
    conn.TypeMap().RegisterType(&pgtype.Type{
        Name:  "uuid_16bytes",
        OID:   pgtype.UUIDOID,
        Codec: &uuidByteArrayCodec{},
    })
    return nil
}
```

Where `uuidByteArrayCodec` translates between `[16]byte` Go side
and the uuid binary format on the wire.

Alternatively, expose a tiny helper:

```go
// db/uuid.go
func UUIDArg(b [16]byte) pgtype.UUID { return pgtype.UUID{Bytes: b, Valid: true} }
```

That's a half-measure but at least gives every repo one shared
import instead of inventing the helper inline.

**Acceptance.**

- [ ] `r.q.Exec(ctx, "INSERT … VALUES ($1)", [16]byte{…})` works
      without an explicit wrap.
- [ ] LicenseKit's `internal/storage/postgres/pg_uuid.go` deletes.

---

## P1-9. `auth.WithAPIKeyHashSecret` Option exists

Already covered as part of P0-2. Listed separately because it's
useful even without the `service.AuthConfig` change — callers who
build `auth.Auth[C]` by hand currently can only set the secret
via `Config.APIKeyHashSecret` field, which is awkward when most
other tunables go through `Option` funcs.

**Acceptance.**

- [ ] `auth.WithAPIKeyHashSecret([]byte("…"))` exists.
- [ ] Used in any kit example that touches API keys.

---

## P1-10. Standard API-key format helper

**Affected paths:** `auth/apikey.go` or new `auth/keygen.go`.

**Problem.** `apikeypg.InsertParams.Prefix` accepts any string.
Every service picks its own plain-key format. Stripe uses
`sk_test_…` / `sk_live_…`; GitHub uses `ghp_…`; LicenseKit picked
`ak_…`. Inconsistent across kit-using services.

**Proposed change.**

```go
// auth/keygen.go (new)

// GenerateAPIKey is the standard kit recipe for minting a plain
// API key. Returns:
//
//   plain  — "ak_<28 url-safe base64 chars>", show to user once
//   hash   — HMAC-SHA256(plain, pepper), 32 bytes, store in DB
//   prefix — first 8 chars of plain ("ak_xxxx"), safe to show in
//            admin UIs without revealing the key
//
// pepper MUST be the same value supplied to auth.Config.APIKeyHashSecret
// (or auth.WithAPIKeyHashSecret) — otherwise the issued key won't
// resolve at login time.
func GenerateAPIKey(pepper []byte) (plain string, hash []byte, prefix string)
```

**Acceptance.**

- [ ] LicenseKit's `internal/app/apikeys.go::APIKeyService.Issue`
      consumes this directly.
- [ ] `kit auth apikey new` CLI uses it.

---

# P2 — Quality of life

## P2-11. `service.WithCORS` env-driven

**Problem.** `service.WithCORS(origins...)` is code-time only. Real
deployments want `CORS_ORIGINS=https://a.com,https://b.com` in env
and have the kit Just Work.

**Proposed change.** Read `CORS_ORIGINS` (comma-separated) from
env inside `service.New`. If set AND no `WithCORS*` option was
passed by the caller, apply it automatically:

```go
type ServiceConfig struct {
    // … existing fields …
    CORSOrigins string `env:"CORS_ORIGINS"` // csv
}
```

`service.New` checks `s.opts.corsConfig == zero && s.cfg.Service.CORSOrigins != ""` and applies `WithCORS(strings.Split(...))`.

**Acceptance.**

- [ ] `CORS_ORIGINS=https://app.acme.com` in env → preflight works
      without code changes.

---

## P2-12. `service.WithExtraValidators(map[string]validator.Func)`

**Problem.** `service.WithValidator(v)` replaces the entire
validator. For "kit defaults + one custom tag" you have to
reconstruct kit's default validator. Common case: registering
`safe_url`, `username`, or domain-specific slug character sets.

**Proposed change.**

```go
func WithExtraValidators(rules map[string]validator.Func) Option {
    return func(o *options) {
        o.extraValidators = rules
    }
}
```

In `service.New`, build the default validator AND register the
extra rules on top.

LicenseKit needs this for a `slug_chars` validator that's safe
across all DB backends.

---

## P2-13. Env-driven Sentry / OTel auto-enable

**Problem.** Today the operator writes:

```go
opts := []service.Option{...}
if dsn := os.Getenv("SENTRY_DSN"); dsn != "" {
    opts = append(opts, service.WithSentry(dsn, sentryOpts))
}
if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
    opts = append(opts, service.WithOtel("svc-name", otelOpts))
}
```

Every service repeats this. Move it inside `service.New`.

**Proposed change.** Inside `service.New`, after applying caller
options:

```go
if s.opts.sentryDSN == "" {
    if dsn := os.Getenv("SENTRY_DSN"); dsn != "" {
        applyWithSentry(s, dsn, SentryOptions{}) // defaults
    }
}
if s.opts.otelServiceName == "" {
    if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
        applyWithOtel(s, defaultServiceName(s.cfg.Service.NodeName), OtelOptions{})
    }
}
```

Skip auto-enable when caller already explicitly opted in; respect
`OTEL_SDK_DISABLED=true` env (W3C-standard kill switch).

**Acceptance.**

- [ ] `SENTRY_DSN=https://…@sentry.io/…` env-only → errors
      captured without any Go-side changes.
- [ ] Default `OTEL_SDK_DISABLED` respected.

---

## P2-14. `service.WithSeed(fn)` — CLI seed mode

**Problem.** Every service eventually grows a `kitctl seed`
subcommand that:
1. Connects to DB / Redis / NATS
2. Runs migrations
3. Inserts demo data
4. Prints a curl cheatsheet

The first three steps are identical to `service.New`. Operators
build a parallel codepath.

**Proposed change.** A new `service.Boot` mode:

```go
func main() {
    service.Boot(func(ctx context.Context) error {
        // normal run path
    },
    service.BootSeed("seed", func(ctx context.Context, svc *Service[...]) error {
        // service.New has already ran; subsystems live; do
        // the seed work, return.
    }))
}

// Invocation:  ./mybinary seed
```

`service.BootSeed` constructs a Service (same as `service.New`)
but skips `svc.Run()` — calls the seed fn instead, then exits.

**Acceptance.**

- [ ] LicenseKit drops its `cmd/licensekitctl` subcommand-dispatch
      boilerplate.

---

## P2-15. `audit` adapter for fibermap

**Affected paths:** new `audit/auditmap/` or `audit/fibermap/`.

**Problem.** `audit` ships event constructors and a Store; emitting
events still requires manual `logger.Log(ctx, event)` calls in
every privileged handler. Want declarative wiring.

**Proposed change.**

```go
type AuditOption struct {
    Action    string                  // e.g. "license.revoke"
    TargetFn  func(c *fiber.Ctx) string // pull target id from path/body
    Metadata  func(c *fiber.Ctx) map[string]any
}

func WithAudit(logger *audit.Logger, opt AuditOption) HandlerOption
```

Then:

```go
fibermap.RegisterHandlerWithParams(eng, "license.revoke",
    h.RevokeLicense,
    fibermap.WithAudit(audit, fibermap.AuditOption{
        Action: "license.revoke",
        TargetFn: func(c *fiber.Ctx) string { return c.Params("id") },
    }))
```

The wrapper logs success/failure outcome after handler returns.

**Acceptance.**

- [ ] LicenseKit can wire audit on its 6 mutating endpoints in
      6 lines, not 60.

---

## P2-16. `service.WithErrorHandler(fiber.ErrorHandler)` opt-in

Already covered as part of P0-3 cleanup. Useful for sentrykit
integration:

```go
service.WithErrorHandler(sentrykit.WrapErrorHandler(fibermap.ErrorHandler(logger)))
```

---

# Documentation gaps

## D-1. "Production deployment checklist" in service/README.md

A short, opinionated section: "if you are wiring `service.New` for
production, you MUST/SHOULD pass these options:"

- `WithBodyLimit(N)` — required for ErrorHandler (until P0-3
  fixes the coupling)
- `WithSentry` / `WithOtel` (or set env vars after P2-13)
- `WithCORS` for browser clients (or env after P2-11)
- `WithMigrations` if your service owns SQL
- `WithRoutes` if you use declarative routing
- `SetBindErrorHandler(fibermap.ErrsvalBindError)` (after P1-4
  ships, optional after)

## D-2. "Common gotchas" page

Surface the trip-wires up-front:
- ErrorHandler coupling to body limit (until P0-3)
- APIKeyHashSecret silence (until P0-2)
- bind error wire shape (until P1-4)
- pgx `[16]byte` to uuid (until P1-8)
- Custom validator tag → panic (always)

## D-3. README quickstart should show typed binding

The current README `RegisterHandler` example uses untyped Fiber
ctx. The `RegisterHandlerWithBody/Query/Params` are arguably the
recommended API surface now — promote them in the quickstart.

---

# Out of scope (intentional non-goals)

- Multi-region deployment patterns
- gRPC / Twirp transport
- GraphQL
- Plugin system

These are deliberate scope decisions for gokit (per its
"composable" framing); restating so contributors don't waste a
PR on them.

---

# Sequencing

Suggested merge order to minimize coordination:

1. P0-1 (bind preserves validator type) — pure additive, no API change.
2. P0-3 (decouple ErrorHandler) — small.
3. P0-2 (APIKeyHashSecret env) — depends on no other.
4. P1-4 (ErrsvalBindError) — depends on P0-1 for full Details.
5. P1-8 (pgx [16]byte) — small db change.
6. P1-5, P1-6 (crypto, ids public packages) — pure additions.
7. P1-7 (WithInput) — fibermap extension.
8. P1-9, P1-10 (auth helpers).
9. P2-* (quality of life) — independent.
10. D-* (docs) — alongside or after P0/P1.

Each P0/P1 item is ~1 PR. Total work surface ≈ 12 PRs.
