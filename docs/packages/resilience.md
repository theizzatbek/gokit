# resilience

Generic resilience primitives at the module root: `breaker/`, `bulkhead/`, `batch/`.

## `breaker/`

Generic three-state circuit breaker (closed/open/half_open) at the module root, peer of `batch/`. `breaker.New(Config) (*Breaker, error)` returns a goroutine-safe breaker.

Config: required `Name` (becomes the `name` const-label on every collector); optional `FailureThreshold` (default 10), `MinimumRequests` (default 20; validated `≥ FailureThreshold`), `WindowDuration`/`WindowSize` (default 10s × 10 buckets — rolling time window), `OpenInterval` (default 30s; constant across re-trips by design — adaptive is v2), `HalfOpenMaxProbes` (default 1; ALL probes must succeed to close; first failure re-opens with a fresh `openedAt`), `IsFailure func(error) bool` (default excludes `context.Canceled` — user cancellation does NOT charge the upstream budget; `DeadlineExceeded` IS counted), `Now` (injectable clock for tests), `Logger`, `Metrics`.

API: `Allow() (allowed bool, done func(success bool))` is the two-phase form (HTTP transports inspect the response before deciding success); `Execute(fn func() error) error` is the ergonomic wrapper that classifies via `IsFailure` and returns `ErrOpen` on short-circuit. `(*Breaker)(nil)` is a safe no-op receiver — Allow always permits, Execute just runs `fn`.

Generation-tagged `done` closure: state mutations bump an internal `gen uint64`, and a stale `done` arriving after a rotation is dropped (kills the "in-flight half-open probe returns success after a new request already re-tripped" race).

Collectors when Metrics is set: `breaker_state{name}` Gauge (0/1/2), `breaker_transitions_total{name,from,to}`, `breaker_short_circuits_total{name}`, `breaker_requests_total{name,outcome}` (`success`/`failure`/`short_circuit`).

Stdlib-only at the package edge — `ErrOpen` is a plain sentinel and the local `breaker.Error{Code, Message}` carries config-validation codes (`breaker_invalid_name`, `breaker_invalid_failure_threshold`, `breaker_invalid_minimum_requests`, `breaker_invalid_window`, `breaker_invalid_open_interval`, `breaker_invalid_half_open_max_probes`). Adapters wrap into `*errs.Error` at their boundary (e.g. `clients/httpc.WithBreaker` surfaces `*errs.Error{KindUnavailable, Code: "httpc_circuit_open"}` wrapping `ErrOpen`, so `errors.Is(err, breaker.ErrOpen)` and `errs.HTTP(err)` both work).

**Adaptive `OpenInterval`:** `Config.OpenIntervalMultiplier` (default 1.0 = constant, back-compat) and `Config.OpenIntervalMax` apply exponential growth across consecutive trips without a successful close in between; reset on close.

**K-of-N half-open:** `Config.HalfOpenSuccessThreshold` (default = `HalfOpenMaxProbes` for back-compat) relaxes the close transition to "K of N must succeed" — any failure still rotates back to open.

**Operator overrides:** `Breaker.ForceOpen(d time.Duration)` jumps to open and pins through the supplied window; `Breaker.ForceClose()` is the manual reset; both fire transition hooks + metrics.

**Lifecycle hook:** `Config.OnStateChange(from, to State)` is panic-safe (callback panics recovered, never blocks the breaker).

**Snapshot:** `Breaker.Stats() Stats` returns `{State, Generation, WindowRequests, WindowFailures, HalfOpenInFlight, HalfOpenSucceeded, OpenedAt, RemainingOpen, ConsecutiveTrips, CurrentOpenInterval, ForcedOpenUntil}` — one mu acquire; nil-receiver safe; for /admin endpoints.

## `bulkhead/`

Generic concurrency-cap with bounded wait queue, peer of `breaker/` at the module root. `bulkhead.New(Config) (*Bulkhead, error)` returns a goroutine-safe bulkhead.

Config: required `Name` (becomes the `name` const-label on every collector) and `MaxConcurrent > 0` (hard cap on in-flight slots); optional `MaxQueue ≥ 0` (default 0 = fail-fast; negative is INVALID — unlimited queue is the failure mode bulkhead exists to prevent), `QueueTimeout` (default 0 = honour only caller ctx; otherwise bounds wait so callers fail fast to a fallback path even when their ctx has more budget), `Logger`, `Metrics`.

API: `Acquire(ctx) (release func(), err error)` is the two-phase form (HTTP transports defer release()); `Execute(ctx, fn func() error) error` is the ergonomic wrapper; `Stats() Stats{InFlight,Waiting,Capacity}` is the cheap snapshot for /healthz. `release` is idempotent via `atomic.Bool` (double-release no-ops; defensive against caller bugs leaking slots). `(*Bulkhead)(nil)` is a safe no-op receiver — Acquire always permits with `noopRelease`.

Implementation is a `sync.Mutex` + `sync.Cond` + integer counter — the chan-based primitive cannot resize in place, which is required for `SetCapacity` / `WithAdaptive`. Hot path acquires the mutex once per Acquire/release; the queue-path watchdog goroutine bridges `ctx.Done()` / `QueueTimeout` to `cond.Broadcast` (sync.Cond does not select on channels). Fairness is best-effort; strict FIFO is intentionally NOT promised. `SetCapacity(n)` is the operator runbook lever — same mutator the adaptive tick loop uses; raising broadcasts to waiters; lowering does NOT preempt in-flight calls (drain on shrink). `Close()` stops the adaptive controller goroutine (no-op for static-mode bulkheads).

Errors: `ErrBulkheadFull` (queue saturated, fail-fast), `ErrQueueTimeout` (QueueTimeout fired), or `ctx.Err()` (caller cancelled).

Slot lifetime = one `Acquire→release` cycle; in the httpc adapter `release` fires on `defer` after `base.RoundTrip` returns, so body-streaming AFTER RoundTrip does NOT hold the slot (the slot tracks the network round-trip, not the body lifetime — otherwise a slow JSON decoder blocks new requests).

Collectors when Metrics is set: `bulkhead_in_flight{name}` Gauge, `bulkhead_waiting{name}` Gauge, `bulkhead_capacity{name}` Gauge (current MaxConcurrent target — moves with WithAdaptive), `bulkhead_acquires_total{name,outcome}` (`ok`/`full`/`ctx_canceled`/`queue_timeout`), `bulkhead_wait_duration_seconds{name,outcome}`, `bulkhead_call_latency_seconds{name}` Histogram (in-flight duration; powers AIMD/Vegas controllers).

Adaptive layer (`WithAdaptive(AdaptiveConfig{Controller, InitialCap, MinCapacity, MaxCapacity, TickInterval, WindowSize})` Option): a background goroutine ticks every TickInterval, builds a `Snapshot{Capacity, InFlight, Waiting, Latency:LatencyStats{P50,P99,Count}, ErrorRate, SinceLast}` from a rolling time-windowed ring buffer, calls `Controller.Next(snapshot)`, clamps the result to [MinCapacity, MaxCapacity], and applies via SetCapacity. Default `AIMDController{IncreaseStep:1, DecreaseFactor:0.5, ErrorThreshold:0.1}` is additive-increase + multiplicative-decrease (TCP congestion control shape) — no-traffic ticks hold (open-circuit period does NOT drive the cap to the floor). Controller is an interface (`Next(Snapshot) int`) so Vegas/Gradient2 land later behind the same shape. Config.MaxConcurrent MUST be 0 when WithAdaptive is set — apimap_invalid_adaptive_config wraps the violation. Execute feeds (fn err == nil) into the latency window as the success bool, so AIMD automatically reacts to fn-failures without caller bookkeeping; two-phase Acquire defaults release to success=true.

Stdlib-only at the package edge — `ErrBulkheadFull` / `ErrQueueTimeout` are plain sentinels; local `bulkhead.Error{Code, Message}` carries config-validation codes (`bulkhead_invalid_name`, `bulkhead_invalid_max_concurrent`, `bulkhead_invalid_max_queue`, `bulkhead_invalid_queue_timeout`, `bulkhead_invalid_adaptive_config`). Adapters wrap at their boundary (clients/httpc maps to `*errs.Error{KindUnavailable, Code: "httpc_bulkhead_full"}` or `KindTimeout, Code: "httpc_bulkhead_queue_timeout"`).

**Lifecycle hook:** `Config.OnCapacityChange(prev, next int)` fires inside `SetCapacity` (manual or adaptive) on a non-trivial change; panic-safe.

**Enhanced `Stats()`:** now includes rolling `LatencyP50` / `LatencyP99` / `AvgWait` / `SampleSize` over `Config.StatsWindow` (default 10s); always-on (independent of `WithAdaptive`); backed by a bounded ring buffer (max 4096 entries) so /healthz reads stay cheap.

## `batch/`

Generic batched-handler dispatcher. `batch.New[T any](Config) (*Batcher[T], error)` returns a goroutine-safe batcher.

Required Config: `HandlerFn(ctx, []T) error` (slice handler — one call per batch), `BatchSize int > 0`. Optional Interval (default 1s), Logger, Metrics.

API: `Submit(item T, ack func(error))` — caller-supplied ack callback fires after the batch's HandlerFn returns, with the same error → atomic ack/nak semantics (all-or-nothing); nil ack is supported for fire-and-forget items. Two flush triggers (interval AND size cap) run in parallel; HandlerFn runs outside the lock so Submit stays unblocked during the round-trip. `(*Batcher)(nil)` safe on Submit/Flush/Close.

Prometheus collectors when Metrics is set: `batch_handlers_total{outcome}`, `batch_items_processed_total`, `batch_handler_duration_seconds`, `batch_batch_size`.

**Resilience pack:** HandlerFn panics are recovered (`runHandlerSafely`) — the panic surfaces as an error to the retry loop and ack callbacks; the flushLoop survives. `Config.MaxPending` (default 0 = unbounded) caps the in-memory buffer; Submit drops + calls ack with `ErrPendingFull` sentinel, `TrySubmit(item, ack) error` returns it synchronously for callers needing the immediate backpressure signal. `Config.MaxInFlightHandlers` (default 1 = sequential — back-compat); > 1 makes Flush async (dispatch spawns into a goroutine; concurrency bounded by a semaphore; Close waits for in-flight). `Config.MaxRetries` + `Config.RetryBackoffBase/Max` add per-batch retry with exponential backoff — ack fires only after the final attempt. `Config.OnBatchStart(ctx, size)` + `Config.OnBatchComplete(ctx, size, err, elapsed)` are panic-safe lifecycle hooks for tracing / audit. `Config.ContextFn func() context.Context` supplies the per-dispatch ctx (tracing-aware); caller `Flush(ctx)` with non-Background still wins. `Batcher.Stats() Stats` returns `{Pending, InFlightHandlers, DispatchedTotal, FailedHandlers, RetriedAttempts}` — one mu acquire; nil-receiver safe.

Used internally by `clients/natsmap`'s batched-handler path; can also be used directly for any source → bulk-sink pipeline.