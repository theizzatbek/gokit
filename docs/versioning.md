# Versioning policy

`gokit` follows [Semantic Versioning 2.0](https://semver.org/). Once
`v1.0.0` is tagged, every release version (`vMAJOR.MINOR.PATCH`)
communicates the magnitude of changes against the previous one. This
file fixes **what counts as a breaking change** so the meaning is
unambiguous.

> **Current status:** `v1.0.0` tagged 2026-06-11. The rules below
> are the active contract — every release from this point forward
> follows them.

## TL;DR

| Change | Bump |
|---|---|
| Adding an exported symbol, Option, env var, or YAML key | **MINOR** |
| Adding a new metric or label value | **MINOR** |
| Adding a new error `Code*` constant | **MINOR** |
| Bug fix that doesn't change exported behaviour | **PATCH** |
| Internal refactor with identical external behaviour | **PATCH** |
| Removing/renaming an exported symbol | **MAJOR** |
| Changing a function signature | **MAJOR** |
| Removing/renaming a metric or label | **MAJOR** |
| Removing/renaming an error `Code*` constant | **MAJOR** |
| Changing the wire shape of YAML configs | **MAJOR** |
| Raising the minimum Go version | **MAJOR** (see exceptions below) |

If you're not sure, treat it as breaking. False-positive caution is
cheap; surprise breakage is expensive.

## What is part of the public API

### 1. Exported Go symbols

Anything that appears in `go doc github.com/theizzatbek/gokit/<pkg>`
is public API. This includes:

- Exported types, functions, methods, constants, variables.
- The exported fields of exported structs.
- Type parameters of exported generic types.
- Embedded types (changing what's embedded can change the method set).

**Internal packages** (under `internal/`) are NOT part of the public
API and may change at any time. Same goes for any package or symbol
explicitly marked `// Internal:` in its doc comment.

### 2. Stable error codes

Every error returned from the kit is `*errs.Error` carrying a stable
string `Code`. The full list of codes is fixed at v1 and forms part
of the API contract — application code may match on them via
`errors.As` + `e.Code == "db_tx_retry_exhausted"`.

- **Additive:** New `Code*` constants are MINOR.
- **Breaking:** Removing or renaming an existing code is MAJOR. Code
  values are caller-facing identifiers; downstream alerting and
  retry-policy depends on them.

### 3. Metric names and label values

The kit emits Prometheus metrics under stable names with stable label
sets. Examples: `db_query_duration_seconds{name,outcome}`,
`auth_apikey_authentications_total{outcome}`,
`breaker_state{name}`, etc.

- **Additive:** New metric names, new label values, new buckets in a
  histogram → MINOR.
- **Breaking:** Renaming a metric, removing a label, changing a
  label's domain (e.g. dropping `outcome=timeout`), splitting one
  metric into many → MAJOR.

Cardinality is the operator's responsibility (`WithQueryName(ctx,
name)` for example), but the *schema* — what labels exist and what
values they can take — is the kit's contract.

### 4. YAML config shape

Every declarative YAML the kit reads (`routes.yaml`, `clients.yaml`,
`subscribers.yaml`, `publishers.yaml`, `crons.yaml`) has a stable
shape backed by JSON Schema files under [`schemas/`](../schemas/).

- **Additive:** New optional fields → MINOR.
- **Breaking:** Renaming/removing a field, changing a field's type,
  making an optional field required → MAJOR.

### 5. Environment variable contract

The kit reads env vars listed in [`README.md`](../README.md) and
per-package READMEs (`DB_URL`, `DB_READ_URLS`, `NATSMAP_SUBSCRIBERS_PATH`,
etc.). The schema for each is part of the contract.

- **Additive:** New env vars → MINOR.
- **Breaking:** Renaming, removing, changing the parsing
  (`comma-separated` → `JSON array`) → MAJOR.

### 6. Database schemas

Per-package DDL (in `auth/refreshpg/schema.sql`,
`auth/apikeypg/schema.sql`, `db/jobs/schema.sql`,
`db/outbox/schema.sql`, `db/inbox/schema.sql`,
`clients/webhooks/storepg/schema.sql`, `audit/auditpg/schema.sql`)
is part of the contract.

- **Additive:** New columns via `ALTER TABLE … ADD COLUMN IF NOT
  EXISTS` → MINOR. Old deployments migrate cleanly when they next
  run the schema file.
- **Breaking:** Dropping a column, renaming a column, changing a
  column type → MAJOR. Migration script + release-note required.

## Specific edge cases

### Adding a required Option

Adding a required parameter to a constructor is a MAJOR change —
existing call sites stop compiling.

Adding a new **optional** Option is MINOR. Same for adding a field
to a config struct with a zero-value default that preserves prior
behaviour.

### Default value changes

Changing the *default* of an existing knob:

- If the new default is observably different (e.g. retry budget
  going from 3 → 5 attempts), it's a **MAJOR** change.
- If it's a perf-only change with identical externally-visible
  behaviour (e.g. internal cache size), it's a **PATCH**.

When in doubt, document the change in the CHANGELOG and pick MAJOR.

### Minimum Go version bumps

Raising the `go` directive in `go.mod`:

- A patch-level bump (`1.26.3` → `1.26.4`) is **PATCH** — same
  language version, just compiler fixes.
- A minor bump (`1.26` → `1.27`) is **MAJOR** in the strictest
  reading — callers on the old minor stop compiling. The kit
  follows the [Go release policy](https://go.dev/doc/devel/release):
  we support the **two most recent Go minor releases**. Bumping
  past two-back constitutes MAJOR.

(The Kubernetes project relaxes this rule, OTel kit follows it
strictly. `gokit` follows it strictly because predictable
build-environments matter more than fast adoption of new Go
features.)

### Behavioural changes that aren't signature changes

Some changes have stable types but new behaviour — e.g. an error
that used to be `KindInternal` now becomes `KindValidation`, or a
log line that used to fire at INFO now fires at WARN.

- If application code might **branch on it** (operator alerting
  matching on `KindInternal`, log-level filters), it's **MAJOR**.
- Pure logging-text changes (the message string of a slog record
  without level/category change) are **PATCH**.

### Removing a `// Deprecated:` symbol

Deprecation lifecycle:

1. Mark with `// Deprecated:` Go doc comment + suggest replacement.
2. Keep through at least one full MINOR cycle (deprecation announce
   ≥ 6 weeks before removal).
3. Remove in the next MAJOR.

Skipping the deprecation step (removing a symbol directly in MAJOR
without a prior `// Deprecated:` marker) is technically permitted by
semver but discouraged — it surprises callers who never saw a
warning.

## CHANGELOG discipline

Every PR that changes externally-observable behaviour MUST add an
entry to `## [Unreleased]` in [`CHANGELOG.md`](../CHANGELOG.md). The
entry goes under one of:

- `Added` — new exported symbols, options, codes, YAML keys.
- `Changed` — observable behaviour changes (default values,
  thresholds, log levels).
- `Deprecated` — symbols marked `// Deprecated:` this release.
- `Removed` — symbols removed (post-MAJOR).
- `Fixed` — bug fixes that don't change documented behaviour.
- `Security` — vulnerability fixes (paired with `SECURITY.md` flow).

When a release tag is cut, `## [Unreleased]` rolls to `## [vX.Y.Z]
— YYYY-MM-DD` and a fresh `## [Unreleased]` is opened on top.

## When to bump MAJOR

A v2 (or v3, etc.) tag should be reserved for changes that genuinely
require call-site migration across multiple packages — a single
breaking change in an obscure subpackage doesn't justify the
upgrade-pain on the rest of the API.

Cluster small breakings into a planned MAJOR release with a written
migration guide. Don't fire MAJOR for every `MAJOR`-classified
change; batch them.

## Coexistence with future MAJOR versions

When a `v2` is cut (per § "When to bump MAJOR"), it lives at the
import path `github.com/theizzatbek/gokit/v2` per Go's standard
module-versioning convention. The kit's commitment to the previous
MAJOR line on a v2 cut:

- `v1.x.y` continues to receive **patch releases for security fixes
  and serious bugs for ≥ 12 months** after the first `v2.0.0` tag.
  Feature work does not backport to `v1` — bug-fix-only window.
- Both major lines coexist in `go.mod` files (different import
  paths via Go's module-version path suffix), so consumers can
  pin per-package and migrate package-by-package rather than big-
  bang.
- The migration guide for `v2` will live alongside the `v2`
  CHANGELOG and call out every `v1`-removed symbol with its `v2`
  replacement.

This is closer to the OTel/Prometheus model than to a hard freeze-
and-cut. The 12-month security window is the contract; `v1` is not
abandoned the moment `v2` lands.

## See also

- [`CHANGELOG.md`](../CHANGELOG.md) — release history.
- [`CONTRIBUTING.md`](../CONTRIBUTING.md) — PR-flow, commit
  conventions.
- [`SECURITY.md`](../SECURITY.md) — vulnerability reporting.
