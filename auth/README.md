# auth

JWT issue/verify (асимметричные EdDSA/ES256), generic `Claims[C]` для app-specific кастомных данных, хеширование паролей argon2id, pluggable refresh-token storage и готовые к монтированию Fiber middleware/handlers (Bearer, RequireScope, RequireRole, Login, Refresh, Logout).

**Импорт:** `github.com/theizzatbek/gokit/auth`
**Зависит от:** `golang-jwt/jwt/v5`, `golang.org/x/crypto/argon2`, `gofiber/fiber/v2`, `github.com/theizzatbek/gokit/errs`

## Зачем это нужно

Auth — это единственный hand-rolled boilerplate, который повторяет каждый сервис: управление JWT-ключами, параметры хеширования паролей, политика ротации refresh-токенов, Bearer-middleware, login/refresh/logout HTTP-хендлеры. `auth` поставляет весь бандл с разумными defaults (Ed25519 + argon2id), pluggable refresh-store (так что вы выбираете Postgres/Redis) и нулевой связностью с остальным китом — `auth` НЕ импортирует `db` или `redis`.

## Quickstart

```go
import (
    "context"
    "time"
    "github.com/theizzatbek/gokit/auth"
    "github.com/theizzatbek/gokit/auth/refreshpg"
)

// 1. Загрузите key material (из PEM env var, secret manager и т.д.)
keySet, err := auth.LoadKeysFromPEM("k1", map[string][]byte{
    "k1": []byte(pemPrivateKey),
})

// 2. Постройте *Auth[YourClaims]
type MyClaims struct {
    Email string `json:"email"`
}
authObj, err := auth.New[MyClaims](auth.Config{
    Issuer:     "myservice",
    Keys:       keySet,
    AccessTTL:  15 * time.Minute,
    RefreshTTL: 30 * 24 * time.Hour,
}, auth.WithRefreshStore(refreshpg.New(db)))

// 3. Напишите свой собственный login-handler. Кит не владеет формой body — вы
//    объявляете LoginRequest сами, верифицируете credentials как угодно
//    (password, mTLS, PKCS7, OIDC, magic link) и передаёте верифицированный
//    LoginResult в IssueLogin. Кит выпускает + persist'ит токены и пишет
//    response {access_token, ...}.
type LoginRequest struct {
    Login    string `json:"login"    validate:"required"`
    Password string `json:"password" validate:"required,min=1"`
}

app.Post("/auth/login", func(c *fiber.Ctx) error {
    var req LoginRequest
    if err := c.BodyParser(&req); err != nil {
        return errs.Wrap(err, errs.KindValidation, "invalid_body", "could not decode body")
    }
    u, err := usersSvc.Authenticate(c.UserContext(), req.Login, req.Password)
    if err != nil {
        return err
    }
    return authObj.IssueLogin(c, auth.LoginResult[MyClaims]{
        Subject: u.ID,
        Custom:  MyClaims{Email: u.Email},
    })
})

// 4. Refresh и logout — однострочники — у них нет service-side логики.
app.Post("/auth/refresh", authObj.IssueRefresh)
app.Post("/auth/logout",  authObj.Logout)

// 5. Защитите роуты
app.Use(authObj.Bearer(auth.BearerRequired))
app.Get("/me", func(c *fiber.Ctx) error {
    p, err := auth.MustFrom[MyClaims](c)
    if err != nil { return err }
    return c.JSON(p.Claims)
})
```

### Кастомные auth-схемы

`IssueLogin` интересуется только верифицированным `LoginResult[C]` — он не видит wire-body. Кастомные схемы (mTLS, PKCS7-signed payloads, OIDC id_token, SAML, SSH-cert, magic links) все следуют тому же паттерну:

```go
app.Post("/auth/login-cert", func(c *fiber.Ctx) error {
    sig, err := parsePKCS7(c.Body())               // ваша верификация
    if err != nil { return errs.Unauthorized(...) }
    subject := extractSubject(sig.Certificate())   // ваш subject-mapping
    return authObj.IssueLogin(c, auth.LoginResult[MyClaims]{
        Subject: subject,
        Custom:  MyClaims{...},
    })
})
```

Для не-Fiber caller'ов (RPC handlers, CLI-тулзы, background-jobs), используйте
`IssueTokens(ctx, res, meta)` / `RotateRefresh(ctx, raw, meta)` напрямую —
они возвращают `TokenPair` и никогда не касаются `*fiber.Ctx`.

## Конфигурация

### `auth.Config`

| Поле | По умолчанию | Заметки |
|---|---|---|
| `Issuer` | — | Обязательно. Идёт в JWT `iss`. |
| `Audience` | nil | Опциональный `aud` allowlist; nil = no audience check |
| `Keys` | — | Обязательно. `*KeySet` — см. "Key management" ниже |
| `AccessTTL` | — | Обязательно (например, 15m). Lifetime access-токена |
| `RefreshTTL` | — | Обязательно (например, 30 * 24h). Lifetime refresh-токена |
| `Leeway` | 1 минута | Clock-skew tolerance во время `exp`/`nbf` валидации |

### Опции

| Опция | По умолчанию | Заметки |
|---|---|---|
| `WithRefreshStore(RefreshStore)` | none | Обязательно для Login/Refresh/Logout. Подключите `refreshpg`/`refreshredis`/свой |
| `WithLogger(*slog.Logger)` | silent | App-level ошибки |
| `WithSecurityLogger(*slog.Logger)` | silent | Security-relevant события. **WARN:** `bearer_verify_failed`, `refresh_reused`. **INFO:** `login_success`, `logout`, `logout_all`. Каждое событие несёт `ip`, `ua`, `path`; INFO-вые добавляют `subject`. См. [Security events](#security-события). |
| `WithMetrics(prometheus.Registerer)` | off | Регистрирует Prometheus-counters: `auth_tokens_issued_total{op}`, `auth_token_issue_failed_total{op,reason}`, `auth_bearer_verify_total{outcome}`, `auth_refresh_total{outcome}`, `auth_logout_total{scope}`, `auth_ratelimit_denied_total`, `auth_idempotency_total{outcome}`. Передайте shared service-registry, так что один scrape покрывает весь кит. RateLimit/Idempotency-counter'ы требуют `*Auth[C]`-bound вариантов (`a.RateLimit`, `a.Idempotency`); package-level свободные функции остаются metric-less by design. |
| `WithCookieDomain(d)`, `WithCookiePath(p)` | "" / "/" | Refresh-cookie scope |
| `WithCookieSecure(bool)` | true | Force/disable `Secure`-flag на refresh-cookie |
| `WithLeeway(d)` | из Config.Leeway | Override leeway после конструкции |

## Key management

`*KeySet` несёт активный signing-key + map verify-only ключей для ротации. Постройте через:

```go
// Из PEM-байтов (production)
ks, err := auth.LoadKeysFromPEM("k1", map[string][]byte{
    "k1": pemActive,
    "k0": pemOld,       // всё ещё верифицировать токены, подписанные под "k0", не подписывать новые
})

// Сгенерировать in-process (для тестов + key-bootstrap)
ks, err := auth.GenerateEd25519Key("k1")
```

Активный key-kid идёт в JWT `kid` header → verify-only сервисы с public-key-only PEM могут валидировать без хранения signing-материала. Ротация non-breaking: задеплойте verifier'ы и со старыми, и с новыми public-key'ями сначала, потом flip активный ключ.

## Common patterns

### Режимы Bearer-middleware

```go
// Required (по умолчанию) — missing или invalid token → 401
app.Use(authObj.Bearer(auth.BearerRequired))

// Optional — missing token = anonymous pass-through, invalid token = всё равно 401
app.Use(authObj.Bearer(auth.BearerOptional))
```

**Важно:** если вы также строите fibermap-engine, установите `auth.BearerOptional` на уровне fiber.App через `fibermap.WithUse(...)`, так что он запускается ДО engine-овского contextInit (который часто читает principal из Locals). Per-route enforcement использует factory-middleware `bearer: []` из `auth/fibermount`.

### Инспектирование аутентифицированного principal'а

```go
// Возвращает ("", false), если нет principal'а
if p, ok := auth.From[MyClaims](c); ok {
    fmt.Println(p.Subject, p.Custom.Email)
}

// Фейлится с *errs.Error{KindUnauthorized}, если отсутствует — используйте после BearerRequired
p, err := auth.MustFrom[MyClaims](c)

// Convenience-аксессоры
subject := auth.Subject[MyClaims](c)         // "" когда нет principal'а
allowed := auth.HasScope[MyClaims](c, "admin:write")
```

### API-key аутентификация

Для service-to-service (B2B) вызовов, где JWT user-flow не подходит. Middleware экстрактит ключ из `X-API-Key` (конфигурируется), HMAC-SHA256-хешит kit-side секретом, lookup'ит [`auth.KeyStore`](apikey.go) реализацию и populate'ит тот же `Principal[C]`, что и Bearer — так что `RequireScope` / `MustFrom` / `HasScope` работают идентично независимо от того, какой путь аутентифицировал запрос.

```go
// 1. Сконфигурируйте секрет (32 случайных байта, относитесь как к signing-key).
authObj, _ := auth.New[MyClaims](auth.Config{
    ...,
    APIKeyHashSecret: cfg.APIKeyHashSecret,
})

// 2. Подключите KeyStore (apikeypg.New(svc.DB) для Postgres или сделайте свой).
store := apikeypg.New(svc.DB)

// 3. Mount middleware. Required + optional режимы оба поддержаны.
app.Use(authObj.APIKey(store))                    // 401 при missing
app.Use(authObj.APIKey(store, auth.WithAPIKeyOptional())) // pass-through при missing
```

YAML route-gating через `api_key` factory:

```yaml
middleware:
  - api_key: []            # обязательно
  - api_key: ["optional"]  # разрешить anonymous
```

Подключите через `auth/fibermount.MountAPIKeyFactory(eng, authObj, store)`.

Хеширование: каждый ключ — это `HMAC-SHA256(plain, APIKeyHashSecret)`. Хеш — это lookup-key — DB-dump alone не раскрывает сырые ключи без kit-секрета. Ротация `APIKeyHashSecret` инвалидирует каждый сохранённый хеш; относитесь как к долгоживущему signing-key. Используйте `auth.HashAPIKey(plain, secret)` на mint-time, так что таблица и verify-путь шарят одну хеш-функцию.

Стабильные error Codes: `api_key_missing`, `api_key_invalid` (existence side-channel подавлен — unknown-ключи возвращают ту же форму, что и missing), `api_key_expired`, `api_key_revoked`.

### Хеширование паролей

```go
hasher := auth.NewHasher(auth.DefaultParams())
hash, err := hasher.Hash("user-password")
if err := hasher.Verify(hashFromDB, "user-password"); err != nil {
    // mismatch — возвращает *errs.Error{KindUnauthorized}
}
// Re-hash на следующий успешный login, если params изменились:
if hasher.NeedsRehash(hashFromDB) { /* re-hash и сохранить */ }
```

`auth.DefaultParams()` — это OWASP-recommended argon2id (memory 64MB, iterations 3, parallelism 4). Override через `auth.NewHasher(auth.Params{...})`, если нужен slower-for-secrecy vs faster-for-throughput.

### Кастомный claims-refresh на /auth/refresh

```go
authObj.SetClaimsRefresher(func(ctx context.Context, subject string) (auth.LoginResult[MyClaims], error) {
    u, err := usersSvc.ByID(ctx, subject)   // re-read текущего user-state'а
    if err != nil { return auth.LoginResult[MyClaims]{}, err }
    return auth.LoginResult[MyClaims]{
        Subject: u.ID,
        Scopes:  u.Scopes,                  // подхватить role-изменения с login'а
        Custom:  MyClaims{Email: u.Email},
    }, nil
})
```

Без `SetClaimsRefresher`, refreshed access-токены несут только Subject ротированной записи и пустые Scopes/Roles/Custom.

### Rate limiting

Token-bucket rate-limiter, монтируемый как plain fiber-middleware или как
fibermap-factory под именем `rate_limit`. Две key-стратегии shipping'ятся
из коробки; принесите свой для кастомных ключей (tenant id, route+IP
tuple и т.д.).

```go
// Per-IP — типично для anonymous-эндпоинтов (login, register).
app.Post("/auth/login",
    auth.RateLimit(5, 10),    // 5 req/s sustained, burst 10
    loginHandler)

// Per-subject (fallback на IP, когда anonymous).
//   Mount auth.Bearer ДО, так что principal populate'д.
app.Use(authObj.Bearer(auth.BearerOptional))
app.Get("/api/heavy", authObj.RateLimitBySubject(2, 5), heavyHandler)

// Кастомный ключ.
app.Post("/webhook",
    auth.RateLimitBy(100, 200, func(c *fiber.Ctx) string {
        return c.Get("X-Tenant-ID")  // tenant-scoped bucket
    }),
    webhookHandler)
```

Декларативно через `routes.yaml` после `fibermount.MountMiddlewareFactories`:

```yaml
groups:
  - prefix: /auth
    routes:
      - method: POST
        path: /login
        handler: users.login
        middleware:
          - rate_limit: ["5", "10"]   # rps, burst — IP-keyed
```

**При превышении лимита:** `*errs.Error{KindRateLimited, Code: "rate_limited"}`
→ HTTP 429 с консервативным `Retry-After` header'ом.

**Memory note:** limiter'ы хранятся в in-process `sync.Map`, keyed by
резолвленным ключом. Никакой eviction'ы. Для сервисов, смотрящих в effectively
unbounded IP-space (публичный интернет, без upstream proxy / WAF), front'ните
кит выделенным rate-limiter'ом (envoy, redis-cell, Cloudflare, …) или
оберните `RateLimitBy` своим LRU + cleanup.

### Idempotency-ключи

`auth.Idempotency(ttl)` — это Fiber-middleware, который deduplicate'ит запросы
write-методов по header'у `Idempotency-Key`. Stripe-style — первый вызов
запускает handler, ответ кешируется на `ttl`, и любой retry с
тем же tuple `(method, path, Idempotency-Key)` replay'ит сохранённый
ответ без re-invoking handler'а. Критично для payment-style
API, где network-retry не должны double-charge'ить.

```go
// Go middleware:
app.Post("/orders",
    auth.Idempotency(24 * time.Hour),
    placeOrder)

// Или через routes.yaml после fibermount.MountMiddlewareFactories:
middleware:
  - idempotency: ["24h"]
```

**Поведение:**
- Запросы **без** header'а `Idempotency-Key` проходят без изменений (opt-in per request).
- Safe-методы (`GET`/`HEAD`/`OPTIONS`) обходят middleware полностью — они уже идемпотентны.
- Handler **ошибки** не кешируются. Transient-failure (`*errs.Error{KindUnavailable}` от flaky-upstream) даёт следующему retry попробовать снова.
- **5xx-ответы не кешируются.** Server-баг может heal; только `2xx`/`3xx`/`4xx` достаточно стабильны для replay.
- Replay'и несут `X-Idempotency-Replay: true`, так что клиенты могут различать.
- Replay'и восстанавливают status, Content-Type, body и маленький allowlist safe-header'ов (`Location`, `X-Request-ID`, `ETag`, `Last-Modified`, `Retry-After`). `Set-Cookie` и Authorization-bound header'ы намеренно НЕ replay'ятся.

**Storage:** по умолчанию in-memory (`sync.Map`, lazy-expiry на Get). Для multi-replica деплоев, где два retry могут приземлиться на разные pod'ы, подключите Redis-backed store:

```go
type redisIdemStore struct{ /* … */ }
func (s *redisIdemStore) Get(ctx, key) (*auth.CachedResponse, bool) { /* HGETALL */ }
func (s *redisIdemStore) Set(ctx, key, resp, ttl) { /* HSET + EXPIRE */ }

app.Use(auth.IdempotencyWithStore(24*time.Hour, &redisIdemStore{...}))
```

### Refresh-token ротация + reuse-detection

Refresh-токены single-use. `auth.IssueRefresh` (или `RotateRefresh` для
не-Fiber caller'ов):
1. Потребляет предоставленный токен (атомарный UPDATE в `refreshpg`, Lua-скрипт в `refreshredis`).
2. Если уже consumed → **revoke'ит всю family** и возвращает `*errs.Error{Code: "refresh_reused"}`. Это ловит replay-атаки.
3. При успехе выпускает новую (access, refresh) пару из той же family.

Установите `WithSecurityLogger(...)`, чтобы получать структурированный WARN каждый раз, когда reuse триггерит family-revoke — полезный alert-сигнал.

### Security события

`WithSecurityLogger(logger)` opts every Auth-метод в структурированную emission событий. Логгер независим от `WithLogger`, так что вы можете shipping'ить его в SIEM / detection-pipeline. Каждое событие — это одна структурированная slog-запись с этими атрибутами:

| Событие | Уровень | Триггер | Атрибуты |
|---|---|---|---|
| `login_success` | INFO | `IssueLogin` успешен | `subject`, `ip`, `ua`, `path` |
| `logout` | INFO | `Logout` revoke'нул refresh-family | `subject`, `ip`, `ua`, `path` |
| `logout_all` | INFO | `LogoutAll` revoke'нул каждый subject-token | `subject`, `ip`, `ua`, `path` |
| `bearer_verify_failed` | WARN | `Bearer`-middleware отклонил token | `err`, `ip`, `ua`, `path` |
| `refresh_reused` | WARN | `IssueRefresh` / `RotateRefresh` увидел re-played token | `err`, `ip`, `ua`, `path` |

`login_failure` — это ответственность caller'а — кит видит только верифицированный `LoginResult`, который вы передаёте в `IssueLogin`. Эмитьте его из вашего handler'а перед вызовом `IssueLogin`.

## Error-модель

| Путь | Error |
|---|---|
| Кастомный login-handler invalid-creds | `*errs.Error{KindUnauthorized, Code: "invalid_credentials"}` (возвращается вашим кодом до достижения `IssueLogin`) |
| Кастомный login-handler bad-body | `*errs.Error{KindValidation, Code: "invalid_body"}` (всё, что возвращает ваш handler; кит не парсит) |
| `IssueRefresh` missing/expired token | `*errs.Error{KindUnauthorized, Code: "missing_refresh"}` / `"refresh_expired"` |
| `IssueRefresh` reuse detected | `*errs.Error{KindUnauthorized, Code: "refresh_reused"}` + family revoked |
| `IssueTokens` / `IssueLogin` / `IssueRefresh` без store | `*errs.Error{KindInternal, Code: "store_unset"}` |
| Store-backend unreachable | `*errs.Error{KindUnavailable, Code: "store_unavailable"}` |
| `Bearer` missing-token (required) | `*errs.Error{KindUnauthorized, Code: "missing_token"}` |
| `Bearer` invalid/expired token | `*errs.Error{KindUnauthorized, Code: "invalid_token"}` |
| `NewHasher` invalid params | ошибка из `validateParams()` |
| `Hash` / `Verify` failure | `*errs.Error{KindInternal}` или `KindUnauthorized` при mismatch |

Все ошибки приземляются в ваш `fibermap.ErrorHandler` и эмитят стандартную wire-форму.

## Wire-формы

### POST /auth/login

```json
// Request
{"login": "a@b.com", "password": "..."}

// Response 200
{
  "access_token": "<JWT>",
  "token_type":   "Bearer",
  "expires_in":   900,
  "subject":      "user-uuid"
}
// Плюс: cookie refresh_token (HttpOnly, Secure, SameSite=Strict по умолчанию)
```

### POST /auth/refresh

Читает cookie `refresh_token`. Возвращает ту же JSON-форму, что и `/auth/login`. Устанавливает НОВУЮ refresh-token cookie.

### POST /auth/logout / /auth/logout/all

Revoke'ит текущий токен (или всю family). Возвращает 204. Очищает cookie `refresh_token`.

## Observability

- `WithLogger(*slog.Logger)` — INFO на issue/refresh, WARN на issuance-ошибках, ERROR на signature-failures
- `WithSecurityLogger(*slog.Logger)` — отдельный stream для security-relevant событий. См. [Security события](#security-события) для схемы. Подключите к вашему SIEM / detection-pipeline.

## Тестирование

Для интеграции с реальным refresh-store используйте per-store testcontainers fixtures (`auth/refreshpg/store_test.go::initPostgresContainer`, `auth/refreshredis/store_test.go::initRedisContainer`).

Для unit-тестов handler'ов, которые принимают `*auth.Auth[C]`, генерируйте ключи in-process:

```go
ks, _ := auth.GenerateEd25519Key("test")
authObj, _ := auth.New[MyClaims](auth.Config{
    Issuer: "test", Keys: ks, AccessTTL: time.Minute, RefreshTTL: time.Hour,
}, auth.WithRefreshStore(refreshmem.New()))  // или refreshpg/refreshredis
```

## Ограничения

- **Нет OAuth/OIDC** — принесите свою provider-интеграцию; `auth` для first-party credentials.
- **Нет multi-factor** из коробки. Добавьте второе middleware, требующее отдельный factor-claim.
- **Нет session-storage'а** — JWT'шки stateless. Используйте Bearer + refresh-ротацию; если нужен server-side session-revocation per-access-token, переключитесь на opaque-токены (вне scope'а здесь).
- **Refresh-cookie browser-targeted.** Mobile/API клиенты должны потреблять cookie-value, или кит нуждается в tweak'е — cookie-path не опциональный.
- **Argon2id memory ≈ 64MB per concurrent hash.** Provisioning соответственно; tune `Params`, если memory-constrained.

## См. также

- [`auth/refreshpg`](refreshpg/README.md) — Postgres-backed `RefreshStore`
- [`auth/refreshredis`](refreshredis/README.md) — Redis-backed `RefreshStore`
- [`auth/fibermount`](fibermount/README.md) — one-call mount `bearer`/`require_scope`/`require_role` factory в fibermap-engine
- [`errs`](../errs/README.md) — error-модель, используемая везде
- [`examples/urlshort`](../examples/urlshort/README.md) — register → login → refresh → Bearer-защищённые роуты
</content>
