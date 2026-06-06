# service

All-in-one bootstrap: бандл `db` + `auth` + `nats` + `apimap` + `fibermap` с авто-детектом опциональности.

## `service/`

All-in-one service helper. `service.New[T, C](ctx, Config, opts...) (*Service[T, C], error)` bundles `*db.DB`, `*auth.Auth[C]`, `*natsclient.Client`, `*http.Client`, `*apimap.Client`, `*fibermap.Engine[T]` with auto-detect optionality (subsystems whose config is empty stay nil).

Auto-mounts `/auth/login` `/auth/refresh` `/auth/logout` as fibermap programmatic routes when Auth is configured. Auto-installs `auth.Bearer(BearerOptional)` at fiber.App level via `WithUse` so the engine's `ContextBuilder` reads JWT subject correctly (fixes the urlshort gotcha). `Run()` blocks with the production-ops bundle (recover + reqlog + metrics + healthz).

Public fields expose every subsystem for direct subpkg access (`svc.DB.Tx(...)`, `svc.Auth.Sign(...)`). Escape hatches: `WithoutAuthHandlers`, `WithoutBearerOptionalLayer`, `WithAPIMapRegistration(fn)` for typed Register* before Build seals the apimap engine, `WithNATSMapRegistration(fn)` for typed Register* before Build seals the natsmap engine, `WithFiberMiddleware`, `WithRunOptions`.

Validation errors use `service_*` Code prefix; subsystem errors propagate as `Cause`. apimap IS auto-given `WithMetrics` — it owns its own `apimap_requests_total{client,endpoint,status}` + `apimap_request_duration_seconds` collectors and intentionally does NOT forward the registry to its internal httpc clients (which would re-register `httpc_*` and panic the shared registry).

**db auto-wiring:** `db.WithMetrics(s.metrics)` is prepended to dbOpts automatically when `s.metrics != nil` so `db_query_duration_seconds` + friends land on the same /metrics scrape as the rest of the kit. Opt out via `WithoutAutoDBMetrics()` — required when caller passes `WithDBOptions(db.WithMetrics(otherReg))` to avoid the duplicate `prometheus.MustRegister` panic on the same registry.

When `NATSMAP_SUBSCRIBERS_PATH` or `NATSMAP_PUBLISHERS_PATH` is set, builds a `clients/natsmap.Runtime` from the YAML and the live NATS client, exposed as `svc.NATSMap`. Cross-validates: NATSMap requires NATS (`service_natsmap_needs_nats`). `Close` drains natsmap before tearing down the NATS connection so in-flight handlers finish first.

`ServiceConfig.ConfigsDir` (env `CONFIGS_DIR`) prefixes every default-named YAML lookup (`routes.yaml`/`clients.yaml`/`subscribers.yaml`/`publishers.yaml`) — set to e.g. `configs` and the kit reads `configs/routes.yaml`, etc. Per-subsystem `Path` overrides bypass the prefix and are honoured literally. Implementation in `service/paths.go::resolvePathInDir`; legacy `resolvePath` is now a thin wrapper passing empty configsDir.
