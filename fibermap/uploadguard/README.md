# fibermap/uploadguard

Upload-validation middleware. Pairs с `clients/s3` для streaming-to-S3. Закрывает классический OWASP file-upload-vector и снимает boilerplate "upload-handling в каждом сервисе своё".

**Импорт:** `github.com/theizzatbek/gokit/fibermap/uploadguard`

## Quickstart

```go
import "github.com/theizzatbek/gokit/fibermap/uploadguard"

app.Post("/avatar",
    uploadguard.Guard("file",
        uploadguard.WithMaxSize(5<<20),                            // 5 MiB
        uploadguard.WithAllowedMIME("image/png", "image/jpeg"),
        uploadguard.WithStreamToS3(svc.S3, func(c *fiber.Ctx) string {
            return "avatars/" + auth.Subject[Claims](c) + ".bin"
        }),
    ),
    func(c *fiber.Ctx) error {
        r, _ := uploadguard.ResultFrom(c)
        return c.JSON(map[string]any{"key": r.S3Key, "size": r.Size})
    })
```

## Checks

| Check | Failure |
|---|---|
| Missing field | 400 `uploadguard_field_missing` (default required) — flip через `WithOptionalField()` |
| Size cap | 400 `uploadguard_size_exceeded` |
| MIME allowlist (sniffed) | 400 `uploadguard_mime_not_allowed` |
| S3 Put error | 500 `uploadguard_upload_failed` |
| Open multipart.FileHeader | 500 `uploadguard_open_failed` |

**MIME — sniffed, не от клиента.** Middleware читает первые 512 байт и dispatch'ит через `http.DetectContentType`. Атакующий, заливающий `evil.php` с `Content-Type: image/png`, упрётся в allowlist'е — реальный sniffed-type будет `text/plain` / `application/octet-stream`.

## Опции

| Опция | Default | Заметки |
|---|---|---|
| `WithMaxSize(bytes)` | 10 MiB | Hard cap. |
| `WithAllowedMIME(...)` | пусто (skip) | Allowlist. Поддерживает `image/*`-wildcard. |
| `WithRequiredField()` | default true | Missing → 400. |
| `WithOptionalField()` | — | Missing → passthrough. |
| `WithStreamToS3(s3, keyFn)` | — | Стримит body напрямую в S3 после validate'а. |
| `WithS3ContentType(ct)` | sniffed | Force-override Content-Type на S3-object'е. |

## Result

После Guard'а handler читает `uploadguard.ResultFrom(c)`:

```go
type Result struct {
    Field      string
    Size       int64
    MIMEType   string                  // sniffed
    FileHeader *multipart.FileHeader   // nil когда WithStreamToS3 consume'ил body
    S3Key      string                  // empty без WithStreamToS3
}
```

## Wildcards в MIME-allowlist

```go
uploadguard.WithAllowedMIME("image/*")           // any image/<subtype>
uploadguard.WithAllowedMIME("image/png", "application/pdf")  // explicit set
```

Wildcard-suffix `/*` matches префикс. Никаких regex / glob — намеренно простой match.

## Ограничения

- **Не делает virus-scan**. ClamAV-интеграция — отдельная (пока) задача; типичный pattern — после `WithStreamToS3` post'ить S3-key в `db/jobs` для async-сканирования.
- **Не валидирует image-dimensions**. Pillow/img-magick — обязанность хендлера. Middleware валидирует, что это _изображение_, не что оно подходящего размера.
- **Single field per Guard**. Multi-file forms — multi-Guard middleware-chain.

## См. также

- [`clients/s3`](../../clients/s3/README.md) — Storage-backend для `WithStreamToS3`
- [`db/jobs`](../../db/jobs/README.md) — async post-processing (image-resize, virus-scan)
</content>
