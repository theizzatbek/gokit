# cmd/kit

`kit` — это операторская CLI для gokit: миграции схемы, генерация
Ed25519-ключей, выпуск API-ключей, инспекция транзакционного outbox'а.
Один бинарь, который оборачивает library entry points кита для ops-использования.

## Установка

```bash
go install github.com/theizzatbek/gokit/cmd/kit@latest
```

## Команды

### `kit version`

```bash
kit version
```

Печатает версию бинаря + VCS revision из `runtime/debug.ReadBuildInfo`.

### `kit migrate`

Обёртка над [`db/migrate`](../../db/migrate/README.md). Полезно как
pre-deployment init-step в K8s, когда миграции достаточно медленные,
что их запуск на старте app раздул бы pod-startup время.

```bash
# Применить pending миграции из ./migrations/.
kit migrate up --dir migrations/ --dsn postgres://...

# Откатить 2 самые недавние.
kit migrate down --steps 2 --dir migrations/

# Инспектировать applied / pending статус.
kit migrate status --dir migrations/

# Печатает текущую версию (или пусто для "ничего не применено").
kit migrate version
```

DSN также может приехать из env `DATABASE_URL`. Миграции следуют
конвенции `NNNN_name.sql` + опциональный `NNNN_name.down.sql`.

### `kit auth keygen`

```bash
kit auth keygen --kid k1 > keys.pem
```

Печатает PKCS8 Ed25519 private + SPKI public PEM в stdout. Pipe в
файл и разнесите в env-переменные, которые ваш сервис ожидает
(конвенция кита: `AUTH_PRIVATE_KEY` для private-половины).

### `kit auth apikey new`

Выпускает свежий API-ключ, привязанный к subject + scopes + role,
HMAC-SHA256'ит kit-секретом, INSERT'ит через
[`auth/apikeypg`](../../auth/apikeypg/README.md), печатает plain key
ОДИН раз.

```bash
export API_KEY_HASH_SECRET=$(openssl rand -hex 32)

kit auth apikey new \
    --subject svc-orders \
    --scopes orders:read,orders:write \
    --role service \
    --expires-in 90d \
    --description "issued by admin@example.com on 2026-06-01" \
    --dsn postgres://...

# Вывод:
# # --- API key (printed ONCE — copy it now) ---
# kit_g7v2y...
#
# # id:          ec79f4a0-...
# # subject:     svc-orders
# # scopes:      orders:read,orders:write
# # role:        service
# # expires_at:  2026-09-01T09:11:24Z
```

Префикс `kit_` позволяет caller'ам чисто grep'ать ключ из логов.
Plain key никогда не persist'ится server-side; только его HMAC живёт
в таблице `auth_api_keys`. Потеряете plain key — оператору придётся
выпустить новый.

### `kit outbox status`

```bash
kit outbox status --dsn postgres://...

# Вывод:
# pending:        12
# oldest_pending: 2026-06-01T09:05:11Z (1m37s ago)
# with_retries:   3
# max_attempts:   8
#
# recent failures:
#   attempts=8 type=orders.created err=nats: jetstream timeout
#   attempts=3 type=orders.updated err=nats: ack timeout
#   ...
```

Первая точка обращения, когда `/readyz` сообщает о фейле outbox-проверки.
Показывает queue-глубину, возраст самой старой pending-строки, топ-5
самых неоднократно зафейлившихся строк с их сообщениями об ошибках.

## DSN-формат

Все DB-связанные команды принимают `--dsn postgres://user:pw@host:port/db?sslmode=disable`
или читают env `DATABASE_URL`. Оба стиля работают — env побеждает,
когда оба не установлены (т.е. флаг пуст).

## Почему без cobra / urfave/cli

Subcommand dispatch живёт в plain stdlib `flag`. Добавление CLI-фреймворка
притащило бы дерево зависимостей больше самой CLI. Trade-off:
никакого автогенерируемого tab-completion'а или fancy help, зато
startup мгновенный и бинарь остаётся под 10 MiB.

## См. также

- [`db/migrate`](../../db/migrate/README.md) — библиотека, которую оборачивает `kit migrate`.
- [`auth/apikeypg`](../../auth/apikeypg/README.md) — KeyStore, в который `kit auth apikey new` INSERT'ит.
- [`db/outbox`](../../db/outbox/README.md) — таблица, которую читает `kit outbox status`.
</content>
