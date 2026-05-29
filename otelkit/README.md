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

## Limitations (v1)

- **Traces only.** No metrics pipeline (kit still uses Prometheus). No logs pipeline.
- **OTLP/HTTP only.** No gRPC exporter (would add `google.golang.org/grpc` to direct deps). Wire it manually via `WithExporterOption` if you really need it.
- **No SDK-level customisation.** SpanProcessor stack is fixed at one Batcher. For multi-pipeline setups (e.g. tee to stdout + collector), construct your own `TracerProvider` and set it with `otel.SetTracerProvider`.
- **No db tracer.** pgx supports a custom tracer, but the kit's `db` package doesn't yet expose it as an option. Add it manually via `pgxpool.Config` if you need DB spans.

## See also

- [`service`](../service/README.md) — `WithOtel(serviceName, ...)` wires otelkit + otelfiber + otelhttp in one option
- [`clients/httpc`](../clients/httpc/README.md) — outbound HTTP transport that otelhttp wraps via `WithBaseTransport`
- [`fibermap`](../fibermap/README.md) — inbound routing layer; otelfiber middleware mounts at the App level