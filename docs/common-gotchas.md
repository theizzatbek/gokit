# Common gotchas

Short reference of trip-wires that bite gokit consumers. Each item carries
**Status** showing which kit version (if any) eliminated the gotcha, so
readers upgrading from older releases can map their pain to a fix.

> Source: gathered from the first-integrator friction report at
> [`docs/v1-followup-licensekit.md`](v1-followup-licensekit.md) (2026-06-12)
> plus eternal-by-design semantic decisions documented in the kit.

| # | Gotcha | Status |
|---|---|---|
| 1 | `*errs.Error` returns surfaced as `text/plain 500` when `WithBodyLimit` wasn't set | **Fixed in v1.0.1** — `ErrorHandler` install no longer gated by body-limit |
| 2 | Raw `[16]byte` query arg → pgx "unable to encode 0x.. into binary format for uuid" | **Fixed in v1.0.1** — `db.Connect` auto-registers `[16]byte` → uuid codec on every fresh connection |
| 3 | `errors.As(err, &validator.ValidationErrors)` empty after `bind.Body/Query/Params/Header` failure | **Fixed in v1.0.1** — bind wraps via `errors.Join` instead of `fmt.Errorf("%w: %v", ...)` |
| 4 | `SENTRY_DSN` / `OTEL_EXPORTER_OTLP_ENDPOINT` in env silently ignored if `WithSentry` / `WithOtel` not called | **Fixed in v1.0.1** — env auto-enable when matching options weren't passed |
| 5 | `auth.APIKeyFactory` panics with `api_key_missing_secret` on first request when `APIKeyHashSecret` empty | **Fixed in v1.1.0** — `AUTH_APIKEY_HASH_SECRET` env, `service.AuthConfig.APIKeyHashSecret` field, and `auth.WithAPIKeyHashSecret([]byte)` Option all thread the same secret slot end-to-end |
| 6 | Default `defaultBindError` writes `{"error":"<full message>"}` plain-text, not `{code, message, details[]}` | **Fixed in v1.1.0** — `fibermap.ErrsvalBindError[T]` is the recommended `SetBindErrorHandler` value; kit default unchanged so the `fibermap` package itself stays errs-convention-free |
| 7 | `WithValidator(v)` replaces kit's default validator instead of extending | **Pending v1.1.0** — P2-12 adds `WithExtraValidators(map[string]validator.Func)` |
| 8 | `CORS_ORIGINS=https://a.com,https://b.com` in env → no CORS wired without an explicit `WithCORS` call | **Pending v1.1.0** — P2-11 auto-applies CORS when env present and `WithCORS*` not passed |
| 9 | Every service rewrites the same AES-256-GCM Seal/Open helper for at-rest secret storage | **Fixed in v1.1.0** — `gokit/crypto.MasterKey` (single-key) + `crypto.Keychain` (kid-routed rotation) are public; `clients/webhooks/storepg`'s private helper is now a thin wrapper |
| 10 | Every service rewrites the same `NewID(prefix)` / `ParseID(prefix, s)` prefixed-ULID utility | **Fixed in v1.1.0** — `gokit/ids` ships with `New` / `Parse` / `Format` + `validate:"id_prefix=prod_"` struct tag |
| 11 | Every service writes ~30 lines of `kitctl seed` subcommand-dispatch boilerplate | **Pending v1.1.0** — P2-14 ships `service.Boot` + `BootSeed` |
| 12 | `audit` events still need manual `logger.Log(ctx, event)` calls in every privileged handler | **Pending v1.1.0** — P2-15 ships `fibermap.WithAudit(...)` `HandlerOption` |
| 13 | Custom validator tag registered after `service.New` panics inside `fibermap.RegisterHandlerWithBody` | **Eternal** — kit's `WithValidator(v)` runs validator wiring at boot; mutating the validator after-boot races with the `bind` package's per-handler use. Solution: register your custom tags BEFORE calling `service.New(...)` (typically wire the validator instance you'll hand off via `WithValidator`). |
| 14 | `service.WithBodyLimit(N)` clobbers caller-supplied `fibermap.WithFiberConfig` via `WithRunOptions` | **Eternal** (until v2 refactor) — service's `WithBodyLimit` wraps fiber config to inject `BodyLimit` + `ErrorHandler`. Caller-supplied `WithFiberConfig` via `WithRunOptions` overwrites that (last-write-wins on fibermap.RunConfig). Solution: if you need both body-limit AND custom fiber config, pass the body-limit IN your own `WithFiberConfig` and skip `WithBodyLimit`. |
| 15 | `fibermap.RegisterHandlerWithInput` exists in v1.0.0 but the four single-source helpers steal the spotlight | **Documented in v1.0.1** — README quickstart now demonstrates `WithInput` for combined body+params/query cases; legacy four single-source helpers stay for the single-input common case |

## How to use this page

- **Hit a symptom?** Search by error string or behaviour.
- **Status column** tells you what version of the kit eliminates the gotcha. `Fixed in v1.0.1` means upgrading is enough. `Pending v1.1.0` means the fix is on the roadmap; you'll need a workaround in the interim (see linked P-item in [`docs/v1-followup-licensekit.md`](v1-followup-licensekit.md)).
- **`Eternal`** means the gotcha is intentional — kit-side semantic decision that won't change. Solution column explains the workaround.

## See also

- [`docs/v1-followup-licensekit.md`](v1-followup-licensekit.md) — full PR-ready
  spec for every `Pending` item above, with reproductions and proposed code
  sketches.
- [`docs/v1-readiness.md`](v1-readiness.md) — pre-v1 audit-close record.
- [`docs/versioning.md`](versioning.md) — semver contract that classifies
  "fix vs breaking change vs additive".
- [`CHANGELOG.md`](../CHANGELOG.md) — per-release diff including the gotcha-
  closing patches above.
