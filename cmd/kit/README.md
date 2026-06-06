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

### `kit doctor`

Hits `/preflight` running service'а и pretty-print'ит per-check
status. Exit-code 0 на ok, 1 на failure, 2 на transport / config-error.
Используется в CI как gate ("staging actually ready before integration tests").

```sh
kit doctor --url http://localhost:8080
kit doctor --url https://staging.api.io --timeout 30s
kit doctor --url http://localhost:8080 --json | jq
```

Требует `service.WithPreflightEndpoint("")` в service-construction'е.

### `kit init`

Scaffold'ит новый kit-based сервис: `go.mod`, `main.go` с
`service.New`, `configs/routes.yaml` + `clients.yaml`, `Makefile`,
`internal/handlers/handlers.go`-stub, `.gitignore`, `README.md`.
Destination-directory должен быть empty.

```sh
kit init tasks --module github.com/acme/tasks
cd tasks
go mod tidy
make run
```

После init'а сервис listens на `:3000` с `/ping` example-route'ом.

### `kit add-endpoint`

Appends route в `configs/routes.yaml` + создаёт handler-stub в
`internal/handlers/<base>.go`. Если файл существует — appends
TODO-comment вместо clobber'а.

```sh
kit add-endpoint GET /tasks tasks.list
kit add-endpoint POST /tasks tasks.create --group /api/v1
```

Handler-base name извлекается из части до первого `.` в handler-name'е.

### `kit gen`

Self-contained генераторы text-assets'ов (SQL templates, YAML манифесты, docker-compose файлы), которые оператор review'ит и коммитит. Никакого AST-rewrite'а, никакого go.mod parsing'а.

#### `kit gen migration <name> [--dir migrations]`

Скаффолдит таймстемпированную пару SQL-файлов:

```sh
kit gen migration add_user_index
  → migrations/20260601120000_add_user_index.up.sql
  → migrations/20260601120000_add_user_index.down.sql
```

Имя должно матчить `[a-z0-9_]+`. Refuses to clobber existing files.

#### `kit gen k8s --name svc --image IMG [flags]`

Эмитит K8s-манифесты для kit-based сервиса: Deployment, Service, ConfigMap (env vars выгребаются из `service.Config` struct tags через reflection), optional Ingress (когда передан `--host`). Probe paths указывают на `/healthz` / `/readyz` / `/preflight`.

```sh
kit gen k8s --name myservice --image myreg/myservice:v1.0 \
  --namespace prod --replicas 3 --host myservice.example.com \
  --out k8s/myservice.yaml
```

#### `kit gen db-cluster [--replicas N] [flags]`

Эмитит `docker-compose.yml` для Postgres primary + N standby со streaming replication из коробки (через bitnamilegacy/postgresql env vars). Используйте для локального dev'а кода, который консьюмит kit's multi-replica routing.

```sh
kit gen db-cluster --replicas 2 --out docker-compose.db.yml
docker compose -f docker-compose.db.yml up -d

# В сервисе:
DB_URL=postgres://app:changeme@localhost:5432/appdb \
DB_READ_URLS=postgres://app:changeme@localhost:5433/appdb,postgres://app:changeme@localhost:5434/appdb \
./my-service
```

| Флаг | По умолчанию | Заметки |
|---|---|---|
| `--replicas N` | 1 | Число standby (min 1). |
| `--image IMG` | `bitnamilegacy/postgresql:16` | Должен поддерживать `POSTGRESQL_REPLICATION_MODE`. |
| `--db NAME` | `appdb` | Имя application БД. |
| `--user USER` | `app` | Application user. |
| `--password PW` | `changeme` | Application password. **Замените для любого persistent setup'а.** |
| `--repl-user USER` | `repl` | Replication-account user. |
| `--repl-password PW` | `changeme` | Replication-account password. |
| `--primary-port PORT` | 5432 | Host port для primary. |
| `--port-base PORT` | 5433 | Host port для первого standby; +N для последующих. |
| `--volume-prefix STR` | `pgdata` | Префикс named volumes (final: `pgdata-primary`, `pgdata-standby-N`). |
| `--out FILE` | stdout | Output-файл. |

Дефолты — dev-friendly, не production-grade. Для prod'а монтируйте секреты через docker secret / external secret store; никогда не бейкайте пароли в committed compose-файлы. Для интеграционного тестирования в Go-коде используйте [`db/testdb.SpinCluster`](../../db/testdb/README.md) который делает то же самое программно через testcontainers.

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
