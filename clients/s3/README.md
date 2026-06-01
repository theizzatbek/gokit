# s3client

Тонкая обёртка над `aws-sdk-go-v2/service/s3`. `s3client.Connect(ctx, Config, opts...)`
открывает клиент; работает и с AWS S3, и с S3-совместимыми
бэкендами (MinIO, Cloudflare R2, DigitalOcean Spaces, Backblaze B2)
через `Endpoint` + `ForcePathStyle`.

**Импорт:** `github.com/theizzatbek/gokit/clients/s3` (пакет `s3client`)
**Зависит от:** `aws-sdk-go-v2/service/s3` + `prometheus/client_golang` + `gokit/errs`

Имя пакета — `s3client`, чтобы не коллизить с upstream-пакетом
`service/s3` (та же конвенция, что и `natsclient` / `redisclient`).

## Quickstart

```go
import s3client "github.com/theizzatbek/gokit/clients/s3"

cli, err := s3client.Connect(ctx, s3client.Config{
    Endpoint:        "http://minio:9000",
    Region:          "us-east-1",
    AccessKeyID:     "minio",
    SecretAccessKey: "minio12345",
    Bucket:          "uploads",
    ForcePathStyle:  true,  // нужно для MinIO
}, s3client.WithLogger(logger), s3client.WithMetrics(promReg))
if err != nil { return err }

// Upload
err = cli.Put(ctx, "avatars/u-42.png", bytes.NewReader(data),
    s3client.WithPutContentType("image/png"),
    s3client.WithPutCacheControl("public, max-age=86400"))

// Presigned download URL для браузера
url, _ := cli.PresignGet(ctx, "avatars/u-42.png", 15*time.Minute)

// Iter listing
for obj, err := range cli.List(ctx, "avatars/") {
    if err != nil { return err }
    fmt.Println(obj.Key, obj.Size)
}
```

`service.New` авто-подключает это, когда `service.Config.S3.Bucket`
установлен — экспонирует результат как `svc.S3`.

## Config

| Поле | Env (с префиксом `S3_` в service) | Заметки |
|---|---|---|
| `Endpoint` | `S3_ENDPOINT` | URL S3 endpoint'а. Пусто → AWS S3 default. Для MinIO/R2 — кастом. |
| `Region` | `S3_REGION` | `us-east-1` по умолчанию. Для S3-совместимых обычно любой валидный токен. |
| `AccessKeyID` / `SecretAccessKey` | `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | Статические credentials. Оба пустые → SDK default-chain (env, instance-profile). |
| `SessionToken` | `S3_SESSION_TOKEN` | Опционально — для временных credentials (STS AssumeRole). |
| `Bucket` | `S3_BUCKET` | Default-bucket каждой операции. Обязательно. |
| `ForcePathStyle` | `S3_FORCE_PATH_STYLE` | Path-style URL'ы (`endpoint/bucket/key`). Нужно для MinIO. |
| `UseSSL` | `S3_USE_SSL` | Hint для SDK при custom-endpoint без scheme. |

## API-поверхность

| Метод | Возвращает | Заметки |
|---|---|---|
| `Put(ctx, key, body, opts...)` | error | PutOption: WithPutContentType, WithPutCacheControl, WithPutContentEncoding, WithPutMetadata. |
| `Get(ctx, key)` | `io.ReadCloser` | Caller MUST close (leak'ит conn иначе). |
| `Delete(ctx, key)` | error | Delete on missing key — НЕ ошибка (S3 semantic). |
| `Exists(ctx, key)` | `(bool, error)` | HeadObject probe. NotFound → `(false, nil)`. |
| `PresignGet(ctx, key, ttl)` | URL | Presigned GET URL для browser-downloads. |
| `PresignPut(ctx, key, ttl)` | URL | Presigned PUT URL для browser-uploads. |
| `List(ctx, prefix)` | `iter.Seq2[Object, error]` | Go-1.23 range-over-func. Early break-safe. |
| `API()` | `*s3.Client` | Escape-hatch — CopyObject, multipart, bucket-admin. |

## Опции

| Опция | Заметки |
|---|---|
| `WithLogger(*slog.Logger)` | Debug на успешные операции (key + elapsed), Warn на ошибки. |
| `WithMetrics(prometheus.Registerer)` | Регистрирует `s3_operations_total{op,outcome}` (counter), `s3_operation_duration_seconds{op}` (histogram), `s3_bytes_transferred_total{direction}` (counter). |

## Error-mapping

| SDK-причина | `*errs.Error` |
|---|---|
| NoSuchKey / NotFound | `KindNotFound`, `s3_not_found` |
| AccessDenied | `KindPermission`, `s3_access_denied` |
| NoSuchBucket | `KindNotFound`, `s3_bucket_not_found` |
| context.Canceled/DeadlineExceeded | `KindTimeout`, `s3_unavailable` |
| прочее | `KindUnavailable`, per-op fallback (`s3_put_failed`, …) |

Оригинальная SDK-ошибка сохраняется как `Cause` — `errors.As` достанет smithy.APIError если нужны детали.

## Ограничения

- **Per-call bucket override не выставлен.** Все операции таргетят `Config.Bucket`. Для multi-bucket workflow'ов конструируйте несколько клиентов.
- **Нет автоматического MIME-detection.** Pass `WithPutContentType` явно — SDK сам не угадывает.
- **Multipart upload — через API().** Кит выставляет один PutObject; для multipart'а (>5GiB) используйте `s3manager.Uploader` поверх `cli.API()`.

## См. также

- [`service`](../../service/README.md) — `service.Config.S3` + авто-проводка
- [`errs`](../../errs/README.md) — error-контракт
</content>
