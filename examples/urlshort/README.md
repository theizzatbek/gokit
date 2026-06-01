# urlshort — gokit интеграционный пример

URL-shortener, использующий каждый пакет gokit в его естественной роли.
Скопируйте `examples/urlshort/` как шаблон при старте нового сервиса.
Вся проводка видна в `main.go` — никакого скрытого DI-контейнера.

## Что он делает

- `POST /auth/register` — создать пользователя (email + пароль, argon2id)
- `POST /auth/login` — выдать access JWT + refresh-cookie (refresh persist'ится в Postgres)
- `POST /auth/refresh` — ротировать refresh-токен, получить свежий access JWT
- `POST /auth/logout` — отозвать refresh-токен
- `POST /links` — сократить URL. Получает `<title>` через `httpc`, плюс description + image через `apimap`, зовущий MicroLink. Публикует `urlshort.link.created` в NATS.
- `GET /{code}` — 302-redirect, инкремент visit-count'а, публикует `urlshort.link.visited`
- `GET /links` — список моих ссылок (auth)
- `GET /links/{code}/stats` — owner-only visit-stats (auth)
- `DELETE /links/{code}` — owner-only delete (auth)
- `GET /healthz`, `GET /metrics` — ops-эндпоинты (авто-подключены через `fibermap.Run`)
- `GET /openapi.json`, `GET /docs` — сгенерированный OpenAPI spec + Scalar UI

## Как запустить

```bash
# 1. Сгенерируйте JWT signing key (PEM Ed25519)
openssl genpkey -algorithm ED25519

# 2. Скопируйте .env.example в .env и вставьте PEM в JWT_PRIVATE_KEY_PEM
cp .env.example .env

# 3. Запустите локальную инфру (Postgres + NATS)
make up

# 4. Запустите сервис
set -a; source .env; set +a
make run
```

### Пример взаимодействия

```bash
# Register
curl -X POST http://localhost:3000/auth/register \
  -H 'content-type: application/json' \
  -d '{"email":"a@b.com","password":"hunter2hunter2"}'

# Login → схватите access-токен
TOKEN=$(curl -s -X POST http://localhost:3000/auth/login \
  -H 'content-type: application/json' \
  -d '{"login":"a@b.com","password":"hunter2hunter2"}' | jq -r .access_token)

# Shorten
curl -X POST http://localhost:3000/links \
  -H "authorization: Bearer $TOKEN" \
  -H 'content-type: application/json' \
  -d '{"url":"https://go.dev"}'

# Перейдите по редиректу
curl -I http://localhost:3000/<code>

# Stats
curl -H "authorization: Bearer $TOKEN" http://localhost:3000/links/<code>/stats
```

## Какой пакет gokit что делает здесь

| Пакет | Роль |
|---|---|
| `gokit/fibermap` | HTTP-роуты, объявленные в `routes.yaml`; `ContextBuilder` инжектит `AppCtx{UserID, Log}` |
| `gokit/fibermap/openapi` | `GET /openapi.json` + `GET /docs` подаются из `Generator.Mount()` |
| `gokit/fibermap/bind` | Декодирование request body + валидация для register/shorten |
| `gokit/errs` | Все service-ошибки — `*errs.Error`; `fibermap.ErrorHandler` маппит в wire-форму |
| `gokit/db` | Postgres-пул + `Query/Exec`; unique-violation всплывает как `errs.AlreadyExists`. `links.ListByUser` использует `ReadQuery`, так что listing едет по реплике, когда `DB_HAS_READ_REPLICA=true` (lag-tolerant read). |
| `gokit/db/sqb` | Squirrel-builder'ы + `sqb.Query/QueryRow/Exec`; каждый SQL в `users/service.go` и `links/service.go` проходит через него (никаких heredoc-строк). |
| `gokit/auth` | JWT issue/verify, argon2id хеширование, `auth.Auth.IssueLogin/IssueRefresh/Logout` (ваш handler парсит body и зовёт их) |
| `gokit/auth/refreshpg` | Refresh-токены persist'ятся в Postgres (таблица `auth_refresh_tokens`) |
| `gokit/auth/fibermount` | Монтирует `bearer`/`require_scope`/`require_role` factory-middleware в engine |
| `gokit/clients/httpc` | `enrich.Fetcher` делает произвольный URL-fetch, чтобы парсить `<title>` из HTML |
| `gokit/clients/apimap` | Декларативный `microlink` клиент; `base_url` из env `${MICROLINK_BASE_URL}` |
| `gokit/clients/nats` | JetStream-публикация `urlshort.link.{created,visited}` на streame `URLSHORT` |
| `gokit/db/outbox` | v2 outbox: `LinkCreated` enqueue'ится через `outbox.EnqueueTyped` ВНУТРИ Create-транзакции; `service.WithOutbox` авто-подключает worker; `pg_notify` будит dispatcher в ~ms от commit'а; 7-day retention sweep'ит published-строки. |
| `gokit/db/migrate` | `service.WithMigrations(embed.FS)` запускает `0001_init.sql` + `0002_idempotent_links.sql` автоматически до того, как любая подсистема прочитает схему — никакого больше ручного `os.ReadFile`/`db.Exec` цикла в `main.go`. |
| `gokit/service.WithCron` / `AddSingletonCron` | Job `daily-stats` логирует link + visit-totals в 03:00 UTC через `pg_try_advisory_lock`-backed singleton — только ОДНА реплика выполняет job на каждый tick в multi-replica деплое. |
| `gokit/cmd/kit` | Операторская CLI: `kit migrate up/down/status`, `kit auth keygen`, `kit auth apikey new`, `kit outbox status`. Pre-deployment миграции + post-incident outbox-инспекция без написания одноразовых Go-бинарей. |
| `gokit/fibermap.LoggerInjector` | Авто-устанавливается `service.New`. `links.Create` зовёт `fibermap.LoggerFrom(c).Info(...)`, чтобы эмитить логи, которые уже несут method, path, request_id, user_id и route — никакого ручного attribute-threading'а. |

## Архитектура

```
                       ┌────────────────────────────────┐
                       │       client (curl / HTTP)      │
                       └──────────────┬─────────────────┘
                                      │
                                      ▼
                          ┌────────────────────────┐
                          │  fiber.App              │
                          │  + Bearer(Optional)     │ ← populates Locals
                          │  + fibermap.Engine[T]   │
                          │  + bearer factory mw    │ ← enforces per-route
                          └───────┬────────────────┘
                                  │
              ┌───────────────────┼──────────────────────────┐
              ▼                   ▼                          ▼
      users.Service        links.Service              auth.Auth[Claims]
       (db)                  (db, enrich,              (refreshpg, hasher)
                              events.PublishCreated,
                              events.PublishVisited)
                                  │
              ┌───────────────────┼────────────────────────┐
              ▼                   ▼                        ▼
      enrich.Fetcher       events.Publishers      gokit/db pool
       (httpc + apimap)     (natsclient)           (pgx)
              │                   │
              ▼                   ▼
      external HTML       NATS JetStream
      + MicroLink          (URLSHORT stream)
```

Bearer-optional слой на `fiber.App.Use` populate'ит `Locals` до того,
как запустится engine'овый `ContextBuilder` — без него `AppCtx.UserID`
был бы пустой в handler'ах (потому что per-route `bearer: []` middleware
запускается ПОСЛЕ `contextInit`). Per-route `bearer: []` всё ещё
enforces 401 на защищённых путях.

### Hot path редиректа

```
GET /:code
  │
  ▼
fibermap.Engine (rate_limit 50/100/IP через auth-factory)
  │
  ▼
links.Service.Resolve
  ├─ Redis cache.Get(code)
  │     positive hit  → return CachedLink
  │     negative hit  → return 404 (no DB)
  │     miss          ↓
  ├─ Postgres SELECT … WHERE code = $1
  │     hit  → cache.Set(code), continue
  │     miss → cache.SetNotFound(code), return 404
  ├─ pub.LinkVisited (fire-and-forget JetStream-публикация)
  ▼
302 Location: original_url
```

Три слоя поглощают scanner / hot-code трафик до того, как он достигнет
Postgres:

1. **Rate limit** на роуте — 50 rps sustained на source IP,
   burst 100. Возвращает 429 с `Retry-After`.
2. **Negative cache** — первая 404 для неизвестного кода сохраняет
   60s sentinel в Redis. Последующие хиты на этот код возвращают 404
   без DB round-trip.
3. **Positive cache** — code → `{ID, UserID, OriginalURL}` cache'ится
   на 1h. `visit_count` + `last_visited_at` намеренно НЕ cache'атся
   (они мутируют на каждый клик; cache'ить их defeat purpose).

Invalidation: `Update` / `Delete` дропают cache-запись после успешной
DB-записи, так что следующий `Resolve` refetch'ит.

env `REDIS_URL` включает кеш; оставив его пустым, fallback'итесь на
прямой Postgres-путь, чтобы пример всё ещё запускался в dev.

### Batched visit counting

`urlshort.link.visited` потребляется через batched-handler mode
natsmap'а: `subscribers.yaml` объявляет `batch_size: 1000` +
`batch_interval: 1s`, и `natsmap.RegisterBatchedHandler[events.LinkVisited]`
привязывает подписчик `link_visit_counter` к `links.VisitCounter.Handle`.
Под капотом natsmap открывает JetStream Pull-подписку, забирает до 1000
сообщений с 1s-deadline и отдаёт их Handle одним срезом. Handler
агрегирует события по code (domain-side решение: много визитов на
популярный код collapse'ятся в одну строку) и запускает ОДИН statement:

```sql
UPDATE links AS l
SET visit_count = l.visit_count + v.delta,
    last_visited_at = greatest(
        coalesce(l.last_visited_at, 'epoch'::timestamptz),
        v.ts)
FROM (VALUES
    ($1::text, $2::bigint, $3::timestamptz),
    ($4,        $5,         $6),
    …
) AS v(code, delta, ts)
WHERE l.code = v.code;
```

Один DB round-trip в секунду, независимо от click-rate. Hot-коды
больше не сериализуются на single row-level write-lock'е — редирект
возвращается за ~1ms даже под нагрузкой.

`subscribers.yaml` объявляет binding только по имени; natsmap
авто-выводит `durable = "link_visit_counter"` и `queue_group =
"link_visit_counter"` (см. `resolveDurableQueueGroup` в
`clients/natsmap/engine.go`), так что горизонтальное масштабирование
никогда не double-count'ит.

**Семантика доставки — at-least-once через JetStream Pull + атомарный
ack.** Batched dispatcher natsmap'а работает в Pull-mode; сообщения
НЕ auto-ack'аются на receipt'е. Возврат handler'а драйвит весь
ack/nak-статус батча:

- `Handle` возвращает nil → кит Ack'ает каждое сообщение в срезе
  (атомарно с DB UPDATE — оба успешны вместе).
- `Handle` возвращает err → кит Nak'ает каждое сообщение; JetStream
  re-deliver'ит весь батч на следующем fetch'е.

Крэш mid-Handle (после `db.Exec`, но до kit'ового ack-walk'а)
результирует в redelivery — DB UPDATE был commit'нут, но ack не был
отправлен. Handler идемпотентен достаточно, чтобы это не имело
значения для visit-count'ов (re-applied UPDATE bump'ает count'у
снова — over-count, никогда не under-count). Strict-once
деплои нуждались бы в отдельной dedup-таблице, keyed by NATS
sequence number; вне scope'а этого примера.

Subscription lifecycle owned by natsmap — `service.Close` зовёт
`Runtime.Drain`, которая останавливает pull-loop и unsubscribe'ит
gracefully. Никакого явного `VisitCounter.Close`.

### Идемпотентный Create

Миграция `0002_idempotent_links.sql` добавляет `UNIQUE (user_id,
original_url)`. `links.Service.Create` pre-check'ит через SELECT и
fallback'ится на fetch-on-conflict, если конкурентный запрос
побеждает гонку — два post'а одного URL от одного пользователя
возвращают тот же code без дубликатных строк.

### Security hardening

Деплоимая поверхность shippится с kit-default OWASP-baseline защитами; этот пример затягивает несколько дополнительных сверху:

- **`/readyz`** — авто-монтируется через `service.New`; запускает DB + NATS + Redis ping параллельно под 5s deadline. Цель K8s readiness probe. Отличается от `/healthz`, который всегда 200.
- **Security headers** — `service.New` авто-устанавливает `fibermap.SecurityHeaders`: HSTS (1y), `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`, API-friendly CSP.
- **64 KiB body limit** — `service.WithBodyLimit(64*1024)` в `main.go`. Fiber возвращает 413 над cap'ом до того, как handler аллоцирует request-буфер.
- **Per-route rate limits** объявлены в `configs/routes.yaml` через auth-factory `rate_limit`:
  - `POST /auth/register` — 1 rps / burst 5 на IP (mass-signup guard).
  - `POST /auth/login` — 2 rps / burst 10 на IP (credential stuffing).
  - `POST /auth/refresh` — 1 rps / burst 5 на IP (cookie probing).
  - `POST /links` — 5 rps / burst 20 на IP (authenticated abuse cap).
  - `GET /:code` — 50 rps / burst 100 на IP (scanner absorption; уже задокументировано выше).

429 от rate limiter'а exposes стабильный `rate_limited` Code, так что client UI может показать "slow down" сообщение, а не утечь "wrong password" подсказку атакующему.

### LinkCreated через transactional outbox

Handler Create запускает INSERT + `outbox.Enqueue` в ОДНОЙ `db.Tx`, так что link-строка и `LinkCreated` событие commit'ятся атомарно. Долгоживущий `outbox.Worker`, запущенный в `main.go`, polling'ит таблицу на 5-секундной cadence, зовёт `natsmap.PublishRaw` на каждое событие и маркирует строку published при успехе — bump'ает `attempts` и stash'ит ошибку в противном случае.

Зачем заморачиваться для click-tracking демки? Потому что **commit→publish crash window** — это именно тот тип бага, который ускользает от интеграционных тестов и всплывает только в production: ссылка durable, downstream "user got their new short URL" notification никогда не срабатывает, никакая ошибка не логируется нигде. outbox толкает publish-шаг в отдельную retryable-транзакцию, так что крэш где угодно в pipeline либо откатывает весь link-create целиком, ЛИБО доставляет событие в итоге.

`LinkVisited` намеренно остаётся на прямом publish-пути — fire-and-forget аналитика, bounded loss при крэше ноды приемлем, а cost storage outbox'а (один INSERT на клик) доминировал бы latency budget hot-path'а редиректа.

## Ограничения

- **Best-effort enrichment:** если MicroLink или target URL лежит, link всё равно создаётся с пустыми метаданными. Это не баг — демка намеренно выбирает "user-visible сбои должны быть громкими; аналитика должна быть тихой".
- **6-char base62 код:** ~1e10 keyspace; retry до 5 раз на unique-violation, потом ошибка. Увеличьте длину для большего объёма.
- **At-most-once visit-counting** во время ≤1s буферного окна (см. выше). Production-деплои, нуждающиеся в strict-count, должны переключиться на manual-ack JetStream-подписку.
- **Нет HTTPS, нет реального secrets handling'а** — только dev.
- **Refresh-token ротация работает**, но нет per-device tracking'а сверх `user_agent`.

## Тесты

```bash
make test    # требует Docker — testcontainers Postgres + NATS + httptest-stub
```

Один end-to-end smoke test (`main_test.go::TestSmoke_EndToEnd`) покрывает
каждый пакет в одном positive-path сценарии. Negative cases живут в
suite-тестов каждого подпакета.
</content>
