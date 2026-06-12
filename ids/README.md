# ids

`gokit/ids` — kit-standard prefixed-ULID utility. API-сервисы на gokit'е tag'ируют идентификаторы коротким type-префиксом (`user_01H…`, `acc_01H…`, `prod_01H…`). Этот pattern был везде, но unstandardised — каждый consumer переписывал `NewID(prefix)` / `ParseID(prefix, s)` / `FormatID(prefix, raw)` поверх `oklog/ulid/v2`. С v1.1.0 это в kit'е.

## Quickstart

```go
import "github.com/theizzatbek/gokit/ids"

id := ids.New("prod_")           // "prod_01H0000000000000000000000000"

raw, err := ids.Parse("prod_", id)  // [16]byte, ready to INSERT into uuid column
// err = *errs.Error{Code: ids.CodeBadPrefix | ids.CodeBadSuffix}

reconstructed := ids.Format("prod_", raw)  // same string as `id`
```

`raw` — это ровно 16 байт, которые pgx пишет в Postgres `uuid` column через `[16]byte` codec, который kit регистрирует в `db.Connect` (см. `db/uuid_codec.go`, шиппит в v1.0.1). То есть `Parse` → `repo.Insert` → `repo.Get` → `Format` round-trip через `uuid` column работает без явной обёртки `pgtype.UUID{}`.

## Wire shape

Каждый ID — это `<prefix><26-char Crockford-Base32 ULID>`. Suffix decodit'ся ровно в 16 raw bytes:

```
6 bytes ms-precision Unix timestamp || 10 bytes randomness
```

- **Time-sortable**: lexicographic order строк совпадает с creation order at ms resolution.
- **Monotonic within process**: два `New()` вызова в одной миллисекунде дают strictly increasing IDs (per ULID monotonic-entropy contract).
- **Goroutine-safe**: `New()` сериализован вокруг package-level mutex'а так что concurrent calls не race'ят entropy source.

## Validator tag

Для declarative-валидации входящих ID в DTO:

```go
v := validator.New(validator.WithRequiredStructEnabled())
ids.RegisterValidator(v)
svc, _ := service.New[AppCtx, Claims](ctx, cfg, service.WithValidator(v))

type CreatePolicyReq struct {
    ProductID string `json:"product_id" validate:"required,id_prefix=prod_"`
}
```

Field, failing tag → normal `validator.ValidationErrors` entry → `fibermap.ErrsvalBindError` picks it up как 400 с per-field `details[]` без дополнительной обвязки.

**Programmatic tag assembly** (для code-generated DTO):

```go
type Field struct {
    ID string `validate:"required,..."`  // assembled at gen time
}

tag := "required," + ids.Tag("prod_")    // ⇒ "required,id_prefix=prod_"
```

## Error codes

Все Parse-ошибки — `*errs.Error{Kind: Validation, Code: ...}`:

| Code | Когда |
|---|---|
| `CodeBadPrefix` (`"id_bad_prefix"`) | Input не начинается с expected prefix'а (включая length mismatch / empty input). |
| `CodeBadSuffix` (`"id_bad_suffix"`) | 26-char tail не валидный Crockford-Base32 ULID (wrong length, illegal character, ULID-overflow). |

Wire-safe для surface'а в HTTP 400 responses.

**Sentinel errors** (для `errors.Is`):

| Sentinel | Wrapping behavior |
|---|---|
| `ErrBadPrefix` | Wrapped by the `*errs.Error` returned from `Parse` when prefix mismatches. |
| `ErrBadSuffix` | Wrapped when suffix isn't a valid ULID. |

Most callers should match on `e.Code` — codes — semver-stable; sentinel identity — нет.

## Конвенции prefix'а

Kit ничего не enforce'ит по содержанию prefix'а — это plain string concatenation:

- Краткие, lowercase, отделены `_` — традиция Stripe (`sk_test_`, `cus_`), GitHub (`ghp_`), и т.д.
- Один префикс на entity-type: `user_`, `acc_`, `prod_`, `pol_`, `lic_`.
- Длина 3-5 chars обычно достаточно — длиннее → IDs становятся unwieldy в URL и логах.

Per-service mapping prefix → entity-type живёт в каком-нибудь `internal/domain/ids.go` каждого сервиса:

```go
package domain

import "github.com/theizzatbek/gokit/ids"

func NewUserID() string    { return ids.New("user_") }
func NewProductID() string { return ids.New("prod_") }
func NewPolicyID() string  { return ids.New("pol_") }

func ParseUserID(s string) ([16]byte, error)    { return ids.Parse("user_", s) }
func ParseProductID(s string) ([16]byte, error) { return ids.Parse("prod_", s) }
```

Этот файл — единственное место в сервисе где префиксы упоминаются как литералы.

## Не для всех ID-кейсов

`ids/` для public-surfaced application-уровневых identifier'ов. НЕ для:

- **Database-row internal IDs** — для `uuid PRIMARY KEY DEFAULT gen_random_uuid()` ничего prefix'ovaть не надо; raw UUID этот case покрывает естественно.
- **Cryptographic tokens** — refresh / access tokens / API keys имеют свои конструкторы (Argon2 / Ed25519 / HMAC), `ids` непригоден.
- **Request IDs** — для request-ID conventions используйте `fibermap.LocalsRequestID` (стандартный X-Request-ID flow).
- **High-entropy nonces** — для one-time tokens используйте `crypto/rand.Read` напрямую; ULID monotonic ordering это leak'ает.
