# clients/apimap

Декларативный исходящий HTTP-слой, симметричный `fibermap` для входящего. Описываете upstream API в YAML (клиенты, эндпоинты, методы, пути, encode/decode, per-endpoint timeout/retry-override'ы, per-client auth); зовёте их по имени в runtime через типизированный `Decode[T]` / `Exchange[Req, Resp]` dispatch.

**Импорт:** `github.com/theizzatbek/gokit/clients/apimap`
**Зависит от:** `gopkg.in/yaml.v3` + `github.com/theizzatbek/gokit/errs` + `github.com/theizzatbek/gokit/clients/httpc`

## Зачем это нужно

`clients/httpc` даёт вам `*http.Client` с retry. Он НЕ решает **endpoint definition** — каждый проект всё ещё руками кодит тот же per-endpoint URL-building, header-setting, error-mapping, body-decoding boilerplate. Этот фрагмент повторяется per-endpoint через каждый сервис. `apimap` — недостающий слой: эндпоинты живут в YAML; код зовёт их по имени. Один grep по `*.yaml` отвечает "какие внешние API этот сервис вызывает?"

## Quickstart

`clients.yaml`:

```yaml
clients:
  - name: github
    base_url: https://api.github.com
    timeout: 10s
    max_retries: 3
    default_headers:
      Accept: application/vnd.github+json
    auth:
      type: bearer
      token: ${GITHUB_TOKEN}
    endpoints:
      - name: get_user
        method: GET
        path: /users/{username}
        decode: json
      - name: create_issue
        method: POST
        path: /repos/{owner}/{repo}/issues
        encode: json
        decode: json
```

`main.go`:

```go
eng := apimap.New()
if err := eng.LoadFile("clients.yaml"); err != nil { return err }
apimap.RegisterResponse[User](eng, "github.get_user")
apimap.RegisterRequest[NewIssue](eng, "github.create_issue")
apimap.RegisterResponse[Issue](eng, "github.create_issue")

client, err := eng.Build(apimap.WithLogger(logger), apimap.WithMetrics(promReg))

user, err := apimap.Decode[User](ctx, client, "github.get_user",
    apimap.Call{Path: map[string]string{"username": "torvalds"}})
```

## YAML-схема

```yaml
clients:
  - name: <string>                          # обязательно, уникально в engine
    base_url: <absolute URL>                # опционально; пропустите для "open client" режима (caller передаёт Call.URL)
    timeout: <duration>                     # опционально → httpc.Config.Timeout
    max_retries: <int>                      # опционально → httpc.Config.MaxRetries
    backoff_base: <duration>                # опционально → httpc.Config.BackoffBase
    backoff_max: <duration>                 # опционально → httpc.Config.BackoffMax
    default_headers:                        # опционально, применяется к каждому эндпоинту
      <Header-Name>: <value>
    auth:                                   # опционально; один из basic|bearer|header|custom|none
      type: basic
      username: <string>
      password: <string>
    # — или —
    #   type: bearer
    #   token: <string>
    # — или —
    #   type: header
    #   name: <Header-Name>
    #   value: <string>
    # — или —
    #   type: custom
    #   name: <signer-id>   # должен совпадать с регистрацией RegisterAuth
    # — или —
    #   type: none
    breaker:                                # опционально; presence включает circuit breaker для апстрима
      failure_threshold: <int>              # default 10
      minimum_requests: <int>               # default 20; должно быть >= failure_threshold
      window_duration: <duration>           # default 10s
      window_size: <int>                    # default 10 (bucket'ов в окне)
      open_interval: <duration>             # default 30s
      half_open_max_probes: <int>           # default 1
    bulkhead:                               # опционально; presence включает concurrency cap
      max_concurrent: <int>                 # обязательно, > 0
      max_queue: <int>                      # default 0 (fail-fast)
      queue_timeout: <duration>             # default 0 (только caller ctx)
    endpoints:
      - name: <string>                      # обязательно, уникально в client'е
        method: GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS
        path: <string с {var}-сегментами>   # например, /users/{username}
        encode: json|form|raw|none          # по умолчанию "none"
        decode: json|raw|none               # по умолчанию "none"
        headers:                            # опционально, мерджится поверх default_headers
          <Header-Name>: <value>
        timeout: <duration>                 # опционально, override client-level
        max_retries: <int>                  # опционально, override client-level
        backoff_base: <duration>            # опционально
        backoff_max: <duration>             # опционально
```

### Env-var substitution

`${VAR_NAME}` где угодно в YAML заменяется из `os.Getenv` на LoadFile-time (regex `[A-Z_][A-Z0-9_]*` — только uppercase). Missing env → `*errs.Error{Code: "apimap_env_var_unset"}`. Используйте это для secrets — никогда не записывайте literal-токены.

### Явные env-значения через `WithEnv`

Если ваш сервис уже имеет typed-config, передайте значения явно вместо опоры на process env:

```go
e := apimap.New(apimap.WithEnv(map[string]string{
    "MICROLINK_BASE_URL": cfg.MicrolinkBaseURL,
}))
e.LoadFile("clients.yaml")
```

`WithEnv` map consult'ируется первым; на miss fallback'ится на `os.LookupEnv`. Оба miss → `apimap_env_var_unset`. Полезно, когда typed-config — source of truth, и вы не хотите apimap-only значениями загрязнять process env.

## Публичный API

```go
type Engine struct{ /* unexported */ }
type Client struct{ /* unexported */ }

// Engine lifecycle (build-once)
func New() *Engine
func (e *Engine) LoadFile(path string) error
func (e *Engine) LoadBytes(b []byte) error
func RegisterRequest[T any](e *Engine, endpoint string)       // опционально — enforces Exchange[T,_]
func RegisterResponse[T any](e *Engine, endpoint string)      // опционально — enforces Decode[T] / Exchange[_,T]
func (e *Engine) Build(opts ...Option) (*Client, error)

// Опции
func WithLogger(*slog.Logger) Option        // → httpc.WithLogger
func WithMetrics(prometheus.Registerer) Option  // → коллекторы apimap_* (НЕ форвардятся в httpc)
func WithBaseTransport(http.RoundTripper) Option // → httpc.WithBaseTransport
func WithHTTPCOptions(...httpc.Option) Option   // прокидка любых httpc-опций (retry policy, middleware, hooks, TLS)
func WithBeforeRequest(func(client, endpoint string, *http.Request)) Option
func WithAfterResponse(func(client, endpoint string, *http.Request, *http.Response, error, time.Duration)) Option
func WithDefaultCall(Call) Option           // engine-wide Call merged before каждый Do/Decode/Exchange

// Engine-level per-client overrides
func (e *Engine) RegisterClientOptions(clientName string, opts ...httpc.Option)
func (e *Engine) RegisterTransport(clientName string, rt http.RoundTripper)   // mock mode
func (e *Engine) SetClientDefaultCall(clientName string, c Call)

// Runtime calls
type Call struct {
    Path    map[string]string  // подстановка {var}; URL-escaped
    Query   url.Values         // мерджится поверх endpoint-defaults (per-key override)
    Headers http.Header        // мерджится последней поверх default + auth + endpoint-headers
    Body    any                // используется Do(); Exchange() принимает body как отдельный аргумент
}

// Untyped — возвращает stdlib *http.Response, caller декодирует + закрывает Body
func (c *Client) Do(ctx context.Context, endpoint string, call Call) (*http.Response, error)

// Typed — использует endpoint.decode, маппит non-2xx в *errs.Error, закрывает Body
func Decode[Resp any](ctx context.Context, c *Client, endpoint string, call Call) (Resp, error)

// Typed с request-body — кодирует per endpoint.encode, декодирует per endpoint.decode
func Exchange[Req, Resp any](ctx context.Context, c *Client, endpoint string, body Req, call Call) (Resp, error)
```

## Common patterns

### Приоритет headers

Когда несколько источников устанавливают тот же header, последний побеждает:

1. `client.default_headers` (YAML)
2. **Auth-derived header** (`Authorization` из `auth:` блока)
3. `endpoint.headers` (YAML)
4. `Call.Headers` (per-call)

Endpoint может override'нуть auth (редко; полезно для debug'а). `Call.Headers` всегда побеждает, так что тесты + per-call override'ы остаются возможны.

### Прокидка httpc-опций (`WithHTTPCOptions` / `RegisterClientOptions`)

`apimap` пропускает только `WithLogger`/`WithMetrics`/`WithBaseTransport` напрямую; всё остальное из `clients/httpc` (retry-policy, middleware chain, hooks, TLS, proxy) подключается через `WithHTTPCOptions`. Engine-wide применяется ко всем clients, per-client overrides приоритетнее.

```go
// Engine-wide: custom retry policy + middleware на всех clients
c, _ := e.Build(
    apimap.WithHTTPCOptions(
        httpc.WithRetryStatusCodes(503, 504),   // 429 не считаем retryable (caller сам handle'ит)
        httpc.WithIdempotencyKeyHeader("Idempotency-Key"),
    ),
)

// Per-client: только Stripe видит свой custom audit middleware
e.RegisterClientOptions("stripe", httpc.WithMiddleware(stripeAuditMW))
```

Build падает с `apimap_unknown_client` если зарегистрировали opts под именем не из YAML — поможет ловить typo'ы.

### Lifecycle hooks (`WithBeforeRequest` / `WithAfterResponse`)

```go
c, _ := e.Build(
    apimap.WithBeforeRequest(func(client, endpoint string, r *http.Request) {
        r.Header.Set("X-Tenant-ID", tenantFromCtx(r.Context()))
    }),
    apimap.WithAfterResponse(func(client, endpoint string, _ *http.Request, resp *http.Response, err error, d time.Duration) {
        auditLog.Record(client, endpoint, statusOf(resp, err), d)
    }),
)
```

Hooks видят kit-стабильный `(client, endpoint)` pair регardless из того, через какой http-client прошёл запрос (per-client base или per-endpoint override). Endpoint имя прокидывается через ctx-key, не через closure. Multiple hooks calls — last wins.

### Default Call (общие headers / queries)

```go
// Engine-wide default
c, _ := e.Build(apimap.WithDefaultCall(apimap.Call{
    Query:   url.Values{"api_version": {"2024-11"}},
    Headers: http.Header{"X-Tenant-ID": {tenantID}},
}))

// Per-client default (после engine, до caller)
e.SetClientDefaultCall("stripe", apimap.Call{
    Headers: http.Header{"Stripe-Version": {"2024-11-20"}},
})

// Layering: engine default → client default → caller's Call (last wins)
```

### Per-endpoint timeout/retry override

```yaml
endpoints:
  - name: list_repos
    method: GET
    path: /users/{user}/repos
    # использует client-level timeout / max_retries
  - name: bulk_index
    method: POST
    path: /search/index
    timeout: 60s       # этот медленный
    max_retries: 0     # этот не должен ретраиться
    encode: json
    decode: json
```

На Build эндпоинты с override'ами получают свой собственный `*http.Client` (через `httpc.New`); эндпоинты без override'ов шарят per-API-client `*http.Client`. Free per-endpoint retry/timeout-политика.

### Open client (ad-hoc URL'ы, multi-host fetcher)

Когда upstream — не один стабильный origin — например, metadata-fetcher, который
тащит произвольные user-supplied URL'ы, webhook-responder, который post'ит на
caller-provided эндпоинты, sandbox/prod switchboard — пропустите `base_url` и
передавайте полный URL per request через `Call.URL`:

```yaml
clients:
  - name: web
    # base_url пропущен → "open client"
    timeout: 10s
    max_retries: 2
    default_headers:
      User-Agent: urlshort/1.0
    endpoints:
      - name: fetch
        method: GET
        decode: raw       # большинство open-client использований — "дай мне body-байты"
```

```go
body, err := apimap.Decode[[]byte](ctx, client, "web.fetch", apimap.Call{
    URL: "https://nytimes.com/some-article",
})
```

**Правила:**
- Два источника URL взаимно исключающие — объявление `base_url` И передача `Call.URL` возвращает `*errs.Error{Code: "apimap_url_conflict"}` на request-time.
- Open client + пустой `Call.URL` → `apimap_missing_request_url`.
- Open client + `Call.Path` → `apimap_unknown_path_var` (нет template'а для подстановки).
- `Call.Query` всё ещё мерджится поверх существующего URL query-string.
- Все client-level knob'ы (timeout, max_retries, backoff, default_headers, auth, custom signers) применяются как обычно.

**Когда предпочесть open client сырому `*http.Client`:** если хотите унифицированную observability кита (slog + Prometheus), `*errs.Error`-маппинг на non-2xx, типизированный `Decode[T] / Exchange[Req,Resp]` API или YAML-level retry/timeout knob'ы — даже когда URL динамический. Если ничего из этого не важно, голый `httpc.New(...)` — на одну indirection меньше.

**Connection-pooling caveat:** open client'ы часто зовут много разных хостов. Go-овый `http.DefaultTransport` pool'ит per-host, так что это нормально для умеренного трафика; если бьёте тысячи различных хостов/sec, тюньте `MaxIdleConnsPerHost` через кастомный transport, переданный через `WithBaseTransport`.

### Auth, объявленный в YAML

Три header-style формы плюс одна extensible-форма:

```yaml
auth: {type: basic,  username: ${BASIC_USER}, password: ${BASIC_PASS}}
auth: {type: bearer, token: ${API_TOKEN}}
auth: {type: header, name: X-API-Key, value: ${API_KEY}}
auth: {type: custom, name: payments_hmac}    # см. ниже
auth: {type: none}                            # или пропустите auth целиком
```

Для `basic` / `bearer` / `header` резолвленный header применяется автоматически перед отправкой. `Call.Headers["Authorization"]` всё ещё может override'нуть per-call.

### Кастомное signing (HMAC, mTLS-signed payloads, request-bound signatures)

Когда upstream нуждается в подписи, зависящей от per-request метода, пути, body, timestamp'а или nonce — всего, что нельзя precompute в статический header — объявите `auth.type=custom` и зарегистрируйте request-mutating функцию под тем же `name`:

```yaml
clients:
  - name: payments
    base_url: ${PAYMENTS_URL}
    auth:
      type: custom
      name: payments_hmac
    endpoints:
      - {name: charge, method: POST, path: /v1/charges, encode: json, decode: json}
```

```go
eng := apimap.New()
_ = eng.LoadFile("clients.yaml")
apimap.RegisterAuth(eng, "payments_hmac", func(req *http.Request) error {
    ts := strconv.FormatInt(time.Now().Unix(), 10)
    // Compute HMAC over method + path + ts + body (читать через GetBody, так
    // что body-stream остаётся доступным для actual send + будущих retries).
    var bodyBytes []byte
    if req.GetBody != nil {
        b, _ := req.GetBody()
        if b != nil {
            defer b.Close()
            bodyBytes, _ = io.ReadAll(b)
        }
    }
    mac := hmac.New(sha256.New, []byte(os.Getenv("PAYMENTS_SECRET")))
    fmt.Fprintf(mac, "%s\n%s\n%s\n", req.Method, req.URL.Path, ts)
    mac.Write(bodyBytes)
    req.Header.Set("X-Timestamp", ts)
    req.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
    return nil
})
client, err := eng.Build(...)
```

**Layering и retries.** Signer сидит *под* retry-слоем httpc — он запускается один раз на network-attempt. Если server возвращает transient 5xx/429 и httpc ретраит, signer re-fires со свежим `*http.Request`, тело которого восстановлено из `req.GetBody`, продуцируя свежую signature/timestamp. Timestamp-bearing схемы с tight clock-skew окнами переживают retries.

**Чтение body.** Используйте `req.GetBody()` (возвращает свежий `io.ReadCloser`) — никогда `req.Body` напрямую, иначе stream потребляется до того, как upstream увидит его. `httpc` populate'ит `GetBody` для всех body-carrying методов.

**Ошибки.** Если `fn` возвращает ошибку, request никогда не уходит; ошибка всплывает как return-value у `Do` / `Decode` / `Exchange`. Оборачивайте `*errs.Error{KindInternal}`, если хотите стабильный Code.

**Build-time валидация.** Если YAML ссылается на `auth.name=foo`, но `RegisterAuth(eng, "foo", ...)` никогда не вызывался, `Build` возвращает `*errs.Error{Code: "apimap_unknown_custom_auth"}`. Дубликат `RegisterAuth` для того же имени panic'ит на registration-time (программерская ошибка).

**Только per-client.** Каждый клиент выбирает свой signer; эндпоинты внутри одного клиента все шарят его signer. Если нужны разные signing-схемы для разных эндпоинтов одного API, разделите на отдельные клиенты.

### Circuit breaker per client

Опциональный `breaker:` блок включает [breaker](../../breaker/README.md)
для всего апстрима — один `*breaker.Breaker` на `clients[].name`. Когда апстрим
падает, breaker размыкается и каждый последующий вызов через любой endpoint
этого клиента возвращается с `*errs.Error{Code: "apimap_<client>_circuit_open"}`
без сетевого вызова, давая апстриму прийти в себя:

```yaml
clients:
  - name: stripe
    base_url: https://api.stripe.com
    timeout: 10s
    max_retries: 3
    breaker:
      failure_threshold: 10       # 10 failure'ов
      minimum_requests: 20        #   в окне как минимум 20 запросов
      window_duration: 10s
      open_interval: 30s
      half_open_max_probes: 1
    endpoints:
      - {name: create_charge, method: POST, path: /v1/charges, encode: json, decode: json}
      - {name: get_charge,    method: GET,  path: /v1/charges/{id}, decode: json}
```

**Unit-of-failure = client, не endpoint.** Если у некоторых endpoint'ов есть
`timeout`/`max_retries` overrides — они получают свой `*http.Client`, но
шарят тот же breaker (потому что upstream падает целиком, а не per endpoint).
Outage Stripe'а триггерит short-circuit и на `create_charge`, и на
`get_charge`.

**Error**: short-circuit'нутый вызов через `Decode`/`Exchange`/`Do` возвращает
`*errs.Error{KindUnavailable, Code: "apimap_<client>_circuit_open"}` с
`Cause = breaker.ErrOpen`. `errors.Is(err, breaker.ErrOpen)` работает после
wrapping'а; `errs.HTTP(err)` даёт 503. Build-time валидация: невалидный YAML
breaker-блок (например, `minimum_requests < failure_threshold`) роняет `Build`
с `apimap_invalid_breaker`.

**Observability**: breaker наследует engine-уровневые `WithLogger` и
`WithMetrics` — `breaker_state{name=<client>}`, `breaker_transitions_total`,
`breaker_short_circuits_total`, `breaker_requests_total` появляются рядом с
`apimap_*` и `httpc_*` на одном scrape'е.

Опущенный `breaker:` блок = breaker выключен (по умолчанию). Это opt-in
feature — клиенты без блока ведут себя в точности как раньше.

### Bulkhead per client

Ортогональный к breaker resilience-pattern (см. [bulkhead](../../bulkhead/README.md)
для деталей). `bulkhead:` блок ограничивает число **одновременных** запросов
к апстриму:

```yaml
clients:
  - name: stripe
    base_url: https://api.stripe.com
    timeout: 10s
    max_retries: 3
    bulkhead:
      max_concurrent: 20         # макс. 20 in-flight
      max_queue: 50              # ещё 50 могут ждать
      queue_timeout: 100ms       # дольше не ждать
    endpoints:
      - {name: create_charge, method: POST, path: /v1/charges}
      - {name: get_charge,    method: GET,  path: /v1/charges/{id}}
```

**Unit-of-failure = client.** Один `*bulkhead.Bulkhead` на `clients[].name`,
расшаренный между endpoint'ами (включая endpoint'ы с httpc-override'ами).
Saturation Stripe'а вызывает fast-fail на ВСЕХ его endpoint'ах сразу — это и
есть цель.

**Errors через `Decode`/`Exchange`/`Do`**:

- Saturated (in-flight + queue cap exceeded): `*errs.Error{KindUnavailable,
  Code: "apimap_<client>_bulkhead_full"}` с `Cause = bulkhead.ErrBulkheadFull`
  → 503 через `errs.HTTP`.
- `QueueTimeout` истёк: `*errs.Error{KindTimeout, Code:
  "apimap_<client>_bulkhead_queue_timeout"}` с `Cause = ErrQueueTimeout` →
  504.
- `errors.Is(err, bulkhead.ErrBulkheadFull)` / `ErrQueueTimeout` работает через
  цепочку Unwrap.

**Совместимость с breaker**: оба блока могут стоять на одном клиенте. Breaker
ловит "Stripe упал" (error rate), bulkhead — "Stripe заел" (concurrency). Они
не пересекаются — bulkhead-слой ВЫШЕ breaker'а, так что открытый breaker не
занимает слот.

**Build-time валидация**: `max_concurrent <= 0` или `max_queue < 0` роняет
`Build` с `apimap_invalid_bulkhead`. `max_queue: -1` (unlimited) намеренно
не поддерживается — это и есть failure mode, который bulkhead предотвращает.

Опущенный блок = bulkhead выключен (opt-in feature; baseline поведение без
изменений).

### Типизированный Register* (опционально, runtime-checked)

`RegisterRequest[T]` / `RegisterResponse[T]` опциональные, но, когда установлены, они bind'ят эндпоинт к специфическому Go-типу. `Decode[U]` / `Exchange[U,V]` потом проверяют, что generic'и вызова матчат регистрацию на runtime:

```go
type IssueResp struct { Number int }
apimap.RegisterResponse[IssueResp](eng, "gh.get_issue")
client, _ := eng.Build(...)

// OK:
out, _ := apimap.Decode[IssueResp](ctx, client, "gh.get_issue", apimap.Call{})

// PANIC'ит на call-time с *errs.Error{Code: "apimap_type_mismatch"}:
_, _ = apimap.Decode[OtherShape](ctx, client, "gh.get_issue", apimap.Call{})
```

Та же проверка на Req-стороне для `Exchange`. Эндпоинты без регистрации принимают любой generic — регистрация opt-in. Build всё ещё валидирует, что каждое зарегистрированное имя существует в YAML (`apimap_registered_endpoint_missing`).

Почему panic, а не возврат ошибки? Потому что Decode[Wrong] — это программерская ошибка — silent JSON-decode неверной формы ведёт к nil-zero'ам в production. Panic высвечивает её на первом тест-ране.

### Режимы encode body

| `encode:` | Принимаемый тип body | Установленный Content-Type |
|---|---|---|
| `none` (по умолчанию) | любой (игнорируется) | — |
| `json` | любой `json.Marshal`-able | `application/json` |
| `form` | `url.Values` или `map[string]string` | `application/x-www-form-urlencoded` |
| `raw` | `io.Reader` | — (caller ставит через Call.Headers) |

Type-mismatch'и возвращают `*errs.Error{Code: "apimap_unsupported_body_type"}`.

### Режимы декодирования response

| `decode:` | Что `Decode[T]` возвращает |
|---|---|
| `none` (по умолчанию) | `Decode[T]` возвращает zero T; body drain'ится |
| `json` | `json.NewDecoder(resp.Body).Decode(&out)` |
| `raw` | T должен быть `[]byte` (всё body) или `io.ReadCloser` (caller закрывает) |

### Маппинг ошибок (non-2xx в `Decode` / `Exchange`)

| Статус | `errs.Kind` | `errs.Error.Code` |
|---|---|---|
| 400 | Validation | `apimap_<client>_<endpoint>_bad_request` |
| 401 | Unauthorized | `apimap_<client>_<endpoint>_unauthorized` |
| 403 | Permission | `apimap_<client>_<endpoint>_forbidden` |
| 404 | NotFound | `apimap_<client>_<endpoint>_not_found` |
| 409 | Conflict | `apimap_<client>_<endpoint>_conflict` |
| 429 | RateLimited | `apimap_<client>_<endpoint>_rate_limited` |
| другие 4xx | Validation | `apimap_<client>_<endpoint>_client_error` |
| 5xx | Internal | `apimap_<client>_<endpoint>_server_error` |

`*errs.Error.Details` несёт `[]FieldError` записи: `{status, url, body (обрезано до 4KB)}`.

`Do()` НЕ конвертирует non-2xx в error — он передаёт `*http.Response` без изменений. Это escape-hatch для streaming-загрузок и кастомного декодирования.

## Build-time валидация

`Engine.Build()` агрегирует каждый validation-failure через `errors.Join`. Codes:

| Code | Когда |
|---|---|
| `apimap_no_clients` | YAML загружен, но `clients:` пуст |
| `apimap_duplicate_client` | два клиента шарят `name` |
| `apimap_duplicate_endpoint` | два эндпоинта шарят `name` в одном клиенте |
| `apimap_missing_client_name` | клиент без `name` |
| `apimap_invalid_base_url` | non-absolute или unparseable URL |
| `apimap_invalid_method` | HTTP-метод вне allowed-set'а |
| `apimap_invalid_path_var` | плохая `{var}`-форма или дубликат в одном path'е |
| `apimap_invalid_encode` / `apimap_invalid_decode` | unknown mode |
| `apimap_auth_invalid_type` | type не в basic/bearer/header/none |
| `apimap_auth_missing_field` | обязательное поле для выбранного типа отсутствует |
| `apimap_env_var_unset` / `apimap_env_var_malformed` | разрешение `${VAR}` зафейлилось |
| `apimap_registered_endpoint_missing` | `Register*` назвал эндпоинт не в YAML |
| `apimap_already_built` | `Build()` вызван дважды |
| `apimap_invalid_breaker` | `breaker:` блок зафейлил `breaker.New` валидацию (например, `minimum_requests < failure_threshold`) |
| `apimap_<client>_circuit_open` | Runtime: вызов через любой endpoint клиента short-circuit'нут открытым breaker'ом. Cause = `breaker.ErrOpen`. |
| `apimap_invalid_bulkhead` | `bulkhead:` блок зафейлил `bulkhead.New` валидацию (например, `max_concurrent ≤ 0`) |
| `apimap_<client>_bulkhead_full` | Runtime: bulkhead saturated. Cause = `bulkhead.ErrBulkheadFull`. |
| `apimap_<client>_bulkhead_queue_timeout` | Runtime: `queue_timeout` сработал прежде, чем освободился слот. Cause = `bulkhead.ErrQueueTimeout`. |

Runtime-коды из `Do`/`Decode`/`Exchange`: `apimap_unknown_endpoint`, `apimap_missing_path_var`, `apimap_unknown_path_var`, `apimap_encode_failed`, `apimap_decode_failed`, `apimap_unsupported_body_type`, `apimap_unsupported_decode_type`, плюс динамические per-endpoint status-коды выше.

## Observability

`WithLogger(*slog.Logger)` проходит насквозь в `clients/httpc` (per-attempt request/retry/exhausted-логи).

`WithMetrics(prometheus.Registerer)` регистрирует apimap-owned коллекторы, keyed by `<client>.<endpoint>`:

| Серия | Labels | Тип |
|---|---|---|
| `apimap_requests_total` | `client`, `endpoint`, `status` | Counter |
| `apimap_request_duration_seconds` | `client`, `endpoint`, `status` | Histogram (дефолтные бакеты) |

`status` бакетизирован (`2xx` / `3xx` / `4xx` / `5xx` / `error`), так что label-cardinality остаётся bounded — transport-failures (timeout, refused, retry-exhausted) приземляются в `error`. Точные status-коды всё ещё живут в per-endpoint `*errs.Error.Code` (например, `apimap_github_get_user_not_found`).

Registry НЕ форвардится в лежащий снизу `clients/httpc`. Более ранние версии форвардили, что заставляло `apimap.WithMetrics(sharedReg)` panic'ить в `service.New` — shared-registry уже держал `httpc_*` из explicit `httpc.New` вызова. С apimap-owned набором, `service.New` авто-применяет `apimap.WithMetrics(svc.Metrics())`, и один `/metrics`-scrape возвращает полную картину: `apimap_*`, `httpc_*`, `db_*`, `nats_*`, `fibermap_http_*`.

## Тестирование

`Engine.RegisterTransport(clientName, http.RoundTripper)` — kit-стандартный mock-hook:

```go
e := apimap.New()
_ = e.LoadBytes(yaml)
e.RegisterTransport("stripe", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
    return &http.Response{
        StatusCode: 200,
        Body:       io.NopCloser(strings.NewReader(`{"id":"ch_mocked"}`)),
        Header:     make(http.Header),
        Request:    req,
    }, nil
}))
client, _ := e.Build()
// Все запросы к `stripe.*` идут через ваш mock; retry/breaker/bulkhead chain всё ещё активен.
```

Build фейлится с `apimap_unknown_client` если зарегистрировали transport под именем не из YAML — typo'ы ловятся сразу.

Альтернатива: override `${MICROLINK_BASE_URL}` (или ваш env var), чтобы указать на `httptest.NewServer`:

```go
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    io.WriteString(w, `{"status":"success","data":{"title":"…"}}`)
}))
t.Cleanup(srv.Close)

t.Setenv("MICROLINK_BASE_URL", srv.URL)
eng := apimap.New()
_ = eng.LoadBytes([]byte(`clients:
  - name: ml
    base_url: ${MICROLINK_BASE_URL}
    endpoints: [{name: get, method: GET, path: /, decode: json}]
`))
apimap.RegisterResponse[Resp](eng, "ml.get")
client, _ := eng.Build()
out, _ := apimap.Decode[Resp](context.Background(), client, "ml.get", apimap.Call{})
```

## Ограничения

- **Нет OpenAPI ingest'а.** Ручной YAML; будущая тулза могла бы вывести из remote API-spec'а.
- **Нет codegen'а.** Только runtime-dispatch — типы регистрируются на старте, не генерируются.
- **Нет hot-reload'а.** YAML грузится один раз на старте.
- **Status-label бакетизирован.** `apimap_requests_total{status=4xx}` — это одна серия для каждого 4xx; per-status-детали принадлежат per-endpoint набору `*errs.Error.Code`, а не label'ам (cardinality control).
- **OAuth2/refresh-token flow вне scope'а.** Используйте `auth:` для одного статического credential'а; для dynamic-secret refresh на каждом вызове (например, periodic token rotation) объявите `auth.type=custom` и пусть ваш signer fetch'ит текущий токен, или оберните `http.RoundTripper` через `WithBaseTransport`.
- **Per-endpoint блоки `auth:` не поддержаны.** Auth — это property upstream API целиком; override per-call через `Call.Headers`.
- **Streaming-загрузки** за пределами `encode: raw` с `io.Reader` вне scope'а.

## См. также

- [`clients/httpc`](../httpc/README.md) — лежащий снизу builder `*http.Client`
- [`errs`](../../errs/README.md) — error-контракт
- [`examples/urlshort`](../../examples/urlshort/README.md) — apimap, зовущий MicroLink в пакете enrich
</content>
