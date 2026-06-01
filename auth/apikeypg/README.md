# auth/apikeypg

Postgres-backed [`auth.KeyStore`](../README.md#api-key-authentication) для API-key middleware кита. Тонкая обёртка над `db.Querier` — владение пулом остаётся за caller'ом.

## Quickstart

```go
// 1. Применить схему (или запихать в свой migration runner).
_, _ = svc.DB.Exec(ctx, apikeypg.Schema())

// 2. Сконструировать store.
store := apikeypg.New(svc.DB)

// 3. Подключить к auth.APIKey + fibermount.
app.Use(authObj.APIKey(store))
fibermount.MountAPIKeyFactory(svc.Engine, authObj, store)

// Admin-путь: выпустить новый ключ.
plain := "ak_" + randomToken()
hash  := auth.HashAPIKey(plain, cfg.APIKeyHashSecret)
id, _ := store.Insert(ctx, apikeypg.InsertParams{
    KeyHash: hash, Subject: "svc-orders",
    Scopes:  []string{"orders:read"}, Role: "service",
    ExpiresAt: time.Now().Add(90*24*time.Hour),
    Description: "issued by admin@example.com on 2026-05-31",
})
// Отдать `plain` caller'у ОДИН раз — только хеш хранится.
```

## API-поверхность

| Метод | Возвращает | Заметки |
|---|---|---|
| `New(q db.Querier) *Store` | — | Конструкция над любым Querier (типично `*db.DB`). |
| `Schema() string` | embedded DDL | Запустите через migration tool или `db.Exec` на boot. |
| `Lookup(ctx, hash) (*KeyRecord, error)` | record или NotFound | Hot path; один `SELECT`. |
| `Insert(ctx, InsertParams) (id, error)` | id новой строки | Возвращает `*errs.Error{KindAlreadyExists}` на коллизии key-hash. |
| `RevokeByID(ctx, id) error` | nil при успехе | Ставит `revoked_at = NOW()`. Идемпотентен против повторного revoke (возвращает `NotFound`). |

## Схема

Колонки `auth_api_keys`:

| Колонка | Тип | Заметки |
|---|---|---|
| `id` | `uuid PRIMARY KEY DEFAULT gen_random_uuid()` | Публичный идентификатор. Появляется в `Principal.JTI`. |
| `key_hash` | `bytea NOT NULL UNIQUE` | HMAC-SHA256 хеш; lookup-индекс. |
| `subject` | `text NOT NULL` | Principal subject (service / user id). |
| `scopes` | `text[] NOT NULL DEFAULT '{}'` | Auth scopes, которые несёт ключ. |
| `role` | `text NOT NULL DEFAULT ''` | Опциональная широкая роль. |
| `description` | `text NOT NULL DEFAULT ''` | Свободный текст для admin/audit. |
| `created_at` | `timestamptz NOT NULL DEFAULT NOW()` | Время выпуска. |
| `expires_at` | `timestamptz` | NULL = без expiry. |
| `revoked_at` | `timestamptz` | NULL = active. |
| `last_used_at` | `timestamptz` | Опционально — кит не бампит на каждом Lookup'е (это превратило бы read в write). При необходимости подключите async writer. |

Два индекса:

- `auth_api_keys_subject_idx (subject)` — admin lookups / revoke-all-for-subject.
- `auth_api_keys_expires_at_idx (expires_at) WHERE expires_at IS NOT NULL AND revoked_at IS NULL` — partial index для nightly expiry-cleanup cron'а.

## Error codes

| Code | Где | Смысл |
|---|---|---|
| `api_key_invalid` | `Lookup`, `RevokeByID` | Нет совпадающей строки (NotFound). Auth-middleware маппит в 401. |
| `apikeypg_insert_failed` | `Insert` | Non-conflict INSERT failure. |
| `apikeypg_lookup_failed` | `Lookup` | Non-NotFound SELECT failure (network / server down). |
| `apikeypg_revoke_failed` | `RevokeByID` | UPDATE failed по причине, отличной от NotFound. |

## Тестирование

Тесты используют `testcontainers-go/modules/postgres` (нужен Docker). Под `-short` пропускаются.

```bash
go test ./auth/apikeypg/...
```

## См. также

- [`auth`](../README.md) — родительский пакет; middleware `auth.APIKey` + интерфейс `auth.KeyStore`
- [`db`](../../db/README.md) — `db.Querier` — то, что `apikeypg.Store` потребляет
- [`auth/refreshpg`](../refreshpg/README.md) — sibling Postgres-адаптер для refresh-token стороны
</content>
