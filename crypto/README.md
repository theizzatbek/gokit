# crypto

`gokit/crypto` — kit-standard primitive для at-rest sealing. AES-256-GCM с двумя API-surface'ами:

- **[`MasterKey`](masterkey.go)** — single-key sealing. Для static-конфигурации (refresh tokens, OAuth tokens, webhook secrets).
- **[`Keychain`](keychain.go)** — kid-routed multi-key sealing. Для rotation: старый и новый ключи коэкзистируют, старые blob'ы остаются расшифровываемыми пока background-job переcheal'ивает их под новый kid.

## Quickstart — MasterKey

```go
import "github.com/theizzatbek/gokit/crypto"

mk, err := crypto.NewMasterKeyFromBase64(os.Getenv("MASTER_KEY"))
if err != nil { return err } // *errs.Error{Code: CodeKeyBase64 | CodeKeyLength}

sealed, err := mk.Seal([]byte("plaintext"))     // → []byte: version || nonce || ct+tag
// store sealed in DB.

plaintext, err := mk.Open(sealed)               // → []byte | *errs.Error{Code: CodeCiphertext}
```

Wire-format MasterKey blob: `[version=0x01] [nonce(12)] [ciphertext+tag(N+16)]`. Self-contained — никаких out-of-band метаданных.

## Quickstart — Keychain (с rotation)

```go
keyV1, _ := crypto.NewMasterKeyFromBase64(os.Getenv("MASTER_KEY_V1"))
_ = keyV1  // unused — handed off via byte map below

kc, err := crypto.NewKeychainFromBase64Map(
    0x01, // active kid — Seal blob'ов под этим kid'ом
    map[byte]string{
        0x01: os.Getenv("MASTER_KEY_V1"),
    },
)

sealed, _ := kc.Seal([]byte("plaintext"))  // blob с kid=0x01 в заголовке
got, _ := kc.Open(sealed)                  // routing по kid из blob'а
```

Wire-format Keychain blob: `[version=0x02] [kid(1)] [nonce(12)] [ciphertext+tag(N+16)]`.

### Rotation workflow

1. **Stage 1:** keychain держит только `keyV1`, active = `0x01`. Все blob'ы `[0x02][0x01][...]`.
2. **Stage 2:** генерируете `keyV2`. Передеплоиваете сервис с keychain'ом `{0x01: keyV1, 0x02: keyV2}`, active = `0x02`. Новые blob'ы `[0x02][0x02][...]`; старые `[0x02][0x01][...]` всё ещё открываются.
3. **Stage 3:** background-job сканирует таблицу, `Open`'ит каждый blob, `Seal`'ит обратно под новым kid'ом.
4. **Stage 4:** job завершился. Передеплой с keychain'ом `{0x02: keyV2}`, active = `0x02`. `keyV1` теперь можно забыть.

Keychain держит до 256 ключей (kid — один byte) — больше любой sane-rotation-policy достигнет.

## Версионирование blob format'ов

Каждый blob carries leading byte:

| Version | Producer | Layout |
|---|---|---|
| `0x01` | `MasterKey.Seal` | `[ver][nonce][ct+tag]` |
| `0x02` | `Keychain.Seal` | `[ver][kid][nonce][ct+tag]` |

`MasterKey.Open` rejects `0x02`-blob'ы; `Keychain.Open` rejects `0x01`-blob'ы. Cross-type isolation — невозможно случайно расшифровать чужой blob.

Future ciphersuite (post-quantum AEADs etc.) → `0x03` + новый constructor + parallel Open path. Existing 0x01 / 0x02 paths остаются untouched.

## Error codes

Construction-time (KindValidation):

| Code | Когда |
|---|---|
| `CodeKeyLength` | Key bytes ≠ 32 |
| `CodeKeyBase64` | Base64 string не декодится ни одним flavour'ом (std/url × padded/raw) |
| `CodeKeychainEmpty` | `NewKeychain` с пустым key map'ом |
| `CodeKeychainNoActive` | Active kid отсутствует в key map'е |

Runtime (KindInternal):

| Code | Когда |
|---|---|
| `CodeSealNonce` | System PRNG failure (kernel-level — НЕ retry locally; surface 503 upstream) |
| `CodeCiphertext` | Sealed blob короче header'а / unknown version / unknown kid / AEAD tag verification failed (wrong key, tampered). **Все failure modes collapse в один code** — callers MUST NOT branch по ним на wire'е (information-leak attacker'у). |

## Конвенции key material

Constructor'ы expectят ровно 32 raw bytes (AES-256). Base64-варианты (`NewMasterKeyFromBase64`, `NewKeychainFromBase64Map`) принимают любой Go stdlib flavour:

- `base64.StdEncoding` (padded `+/=`)
- `base64.URLEncoding` (padded `-_=`)
- `base64.RawStdEncoding` (unpadded `+/`)
- `base64.RawURLEncoding` (unpadded `-_`)

Operators copy-paste из произвольного key-management UI — kit подбирает flavour автоматически. Decoded bytes всё равно должны быть 32 — `CodeKeyLength` иначе.

## Goroutine safety

`MasterKey` / `Keychain` — safe для concurrent use после construction. Underlying `cipher.AEAD` — goroutine-safe per stdlib contract, и оба типа не держат мутабельного состояния.

## Внутренний `clients/webhooks/storepg`

`webhooks/storepg` имел свой private AES-GCM helper. С v1.1.0 это thin-wrapper над `gokit/crypto.MasterKey` который translate'ит coded errors к `webhooks.CodeStorepgNoKey` / `CodeStorepgDecryptFailed` для wire-compat с downstream alerting. Новый код должен напрямую использовать `gokit/crypto`.

## Не для public-API-secret'ов

`crypto` — для AT-REST sealing (DB-rows, файлы). НЕ для:

- API key hashing → используйте `auth.WithAPIKeyHashSecret` (HMAC-SHA256, не AEAD)
- Password hashing → используйте `auth.Hasher` (Argon2)
- JWT signing → используйте `auth.LoadKeysFromPEM` (Ed25519)
- Webhook signature verification → используйте `clients/webhooks/verifiers` (HMAC, ECDSA в зависимости от vendor'а)
