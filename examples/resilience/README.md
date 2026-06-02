# examples/resilience

Single-process демо kit'овой outbound-HTTP resilience-цепочки: декларативный
`apimap` + circuit breaker + bulkhead, всё через YAML.

## Запуск

```bash
go run ./examples/resilience
```

Никакого docker, никаких external dependencies — `httptest.Server` симулирует
flaky upstream прямо в процессе.

## Что показывает

1. `clients.yaml` — один client `flaky` с двумя resilience-блоками:
   ```yaml
   breaker:
     failure_threshold: 3
     minimum_requests: 5
     window_duration: 5s
     open_interval: 2s
   bulkhead:
     max_concurrent: 5
     max_queue: 10
   ```
2. `apimap.Engine.Build()` авто-врапает каждый endpoint в `httpc → bulkhead → breaker → base`. Caller не пишет ни строчки adapter-кода.
3. Демо стреляет 30 параллельными `Decode[pingResponse]` вызовами; апстрим:
   - каждый 3-й request → 503
   - каждый 5-й → 300ms sleep + 200
   - остальные → 200
4. В конце печатает Prometheus collectors — видно, как breaker открылся,
   bulkhead'у fail-fast'нул лишние waiter'ы.

## Типичный output

```
Batch 1 — server is healthy-ish; some 5xx, some slow:
  ok=8  circuit_open=4  bulkhead_full=15  other=3

Batch 2 — wait 3s past the breaker's OpenInterval=2s, expect recovery:
  ok=0  circuit_open=29  bulkhead_full=0  other=1

Final collector snapshot:
  ...
  breaker_state{name=flaky} 1                                    # 1 = open
  breaker_transitions_total{from=closed,to=open,name=flaky} 1
  breaker_transitions_total{from=open,to=half_open,name=flaky} 1
  breaker_transitions_total{from=half_open,to=open,name=flaky} 1 # probe failed → re-open
  breaker_short_circuits_total{name=flaky} 33
  bulkhead_acquires_total{outcome=full,name=flaky} 15
  bulkhead_acquires_total{outcome=ok,name=flaky} 45
  bulkhead_capacity{name=flaky} 5
```

Числа варьируются между запусками (Go `select` рандомен), но картина устойчива:

- Breaker **trip'ается** после ~5 калов в batch 1 (видно по
  `closed→open` transition).
- В batch 2 после `OpenInterval=2s` пускает одну **probe**, она снова падает
  (server ещё flaky) → `half_open→open` → остаток batch 2 short-circuit'ится.
- Bulkhead отказывает в 15 случаях из batch 1 — это callers, упавшие за `MaxConcurrent + MaxQueue = 15`.

## Уроки

- **Туго настроенный bulkhead "глушит" breaker.** Если `MaxConcurrent` сильно
  меньше скорости поступления callers, breaker не получает достаточно
  observations чтобы trip'нуться — bulkhead fail-fast'ит их раньше. Решение —
  ослабить bulkhead или ужесточить breaker (lower `failure_threshold`).
- **Breaker не reset'ит сам себя по успешным calls — только probe.** В batch
  1 даже после получения зеленых ответов breaker может остаться open до
  истечения `OpenInterval`.
- **`apimap` collectors самостоятельно** считают `status=2xx/5xx/error`
  buckets независимо от breaker/bulkhead — полезно для аудита "сколько
  запросов upstream вообще обработал".

## См. также

- [`breaker`](../../breaker/README.md) — 3-state state machine.
- [`bulkhead`](../../bulkhead/README.md) — concurrency cap + adaptive option.
- [`clients/apimap`](../../clients/apimap/README.md) — declarative outbound API.
