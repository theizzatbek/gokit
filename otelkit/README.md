# otelkit

Thin OpenTelemetry tracing bootstrap for kit-based services. One call
sets up a TracerProvider exporting via OTLP/HTTP, wires it as the
process-global tracer + propagator, and returns a shutdown function
callers register with their cleanup path.

**Import:** `github.com/theizzatbek/gokit/otelkit`
**Depends on:** `go.opentelemetry.io/otel/{sdk,exporters/otlp/otlptrace/otlptracehttp,propagation,...}`

## Quickstart

```go
shutdown, err := otelkit.Setup(ctx, "urlshort",
    otelkit.WithServiceVersion("1.0.0"),
    otelkit.WithSampleRatio(0.1),
    otelkit.WithResourceAttribute("deployment.environment", "production"),
)
if err != nil { return err }

defer func() {
    sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    _ = shutdown(sctx)
}()
```

For the typical kit service, `service.WithOtel(serviceName, ...)` wraps
this plus otelfiber + otelhttp transport wiring in one line — see
[service README](../service/README.md).

## Configuration

The OTLP/HTTP exporter reads the standard OTel environment variables —
no kit-specific knobs:

| Env | Purpose |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Collector endpoint (e.g. `http://otel-collector:4318`) |
| `OTEL_EXPORTER_OTLP_HEADERS` | Extra headers (auth tokens, tenant ids) |
| `OTEL_EXPORTER_OTLP_COMPRESSION` | `gzip` or `none` |
| `OTEL_RESOURCE_ATTRIBUTES` | Resource attrs merged into every span |

For values the kit reads directly:

| Option | Default | Notes |
|---|---|---|
| `Setup(ctx, serviceName)` | — | Required. Populates `service.name`. Empty value returns an error. |
| `WithServiceVersion(v)` | "" | Sets `service.version` on the resource |
| `WithSampleRatio(r)` | 1.0 | Head-based sampling ratio (0..1) |
| `WithResourceAttribute(k, v)` | — | Append a constant attribute (region, az, cluster) |
| `WithExporterOption(opt)` | — | Forward an `otlptracehttp.Option` for endpoint/headers in code |

## Behaviour

- **Propagation:** W3C TraceContext + W3C Baggage. Inbound requests carrying `traceparent` continue the trace; outbound calls inject it via `otelhttp`.
- **Sampler:** ratio-based when < 1.0; `AlwaysSample` when ≥ 1.0.
- **Batcher:** 5s flush window. Pending spans flush during `shutdown(ctx)` — bound a finite deadline before calling, otherwise an unresponsive collector blocks indefinitely.
- **Idempotent shutdown:** the returned function is `sync.Once`-guarded.

## Metrics

`otelkit.SetupMetrics(ctx, serviceName, promRegistry, opts...)` opens a
second OTLP/HTTP pipeline that **bridges** the kit's Prometheus
collectors onto OTel periodic push. This way the existing
`db_*`/`httpc_*`/`nats_*`/`apimap_*`/`auth_*`/`fibermap_http_*`
instrumentation lands at the same OTel collector as the traces — no
need to rewrite the kit's metric instrumentation in OTel APIs.

```go
shutdown, err := otelkit.SetupMetrics(ctx, "urlshort", svc.Metrics().(prometheus.Gatherer),
    otelkit.WithMetricsInterval(30 * time.Second),
    otelkit.WithMetricsServiceVersion("1.0.0"),
)
```

| Option | Default | Notes |
|---|---|---|
| `WithMetricsServiceVersion(v)` | "" | `service.version` on the metric resource |
| `WithMetricsResourceAttribute(k, v)` | — | Append a constant attribute (region, az, cluster) |
| `WithMetricsExporterOption(opt)` | — | Forward an `otlpmetrichttp.Option` for endpoint/headers in code |
| `WithMetricsInterval(d)` | 60s | PeriodicReader push interval |

`service.WithOtel` auto-wires `SetupMetrics` whenever the service
registry is a `prometheus.Gatherer` (the default
`prometheus.NewRegistry()` is). Disable via
`service.WithoutOtelMetrics()` when the deployment already scrapes
`/metrics` and doesn't want a parallel push pipeline.

## Limitations

- **Logs pipeline out of scope.** Add manually if needed.
- **OTLP/HTTP only.** No gRPC exporter (would add `google.golang.org/grpc` to direct deps). Wire it manually via `WithExporterOption` / `WithMetricsExporterOption` if you really need it.
- **No SDK-level customisation.** SpanProcessor stack is fixed at one Batcher; metric pipeline is fixed at one PeriodicReader. For multi-pipeline setups, construct your own `TracerProvider` / `MeterProvider` and call `otel.SetTracerProvider` / `otel.SetMeterProvider` directly.
- **No db tracer.** pgx supports a custom tracer, but the kit's `db` package doesn't yet expose it as an option. Add it manually via `pgxpool.Config` if you need DB spans.

## See also

- [`service`](../service/README.md) — `WithOtel(serviceName, ...)` wires otelkit + otelfiber + otelhttp in one option
- [`clients/httpc`](../clients/httpc/README.md) — outbound HTTP transport that otelhttp wraps via `WithBaseTransport`
- [`fibermap`](../fibermap/README.md) — inbound routing layer; otelfiber middleware mounts at the App level