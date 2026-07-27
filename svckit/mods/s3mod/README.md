# svckit/mods/s3mod

S3-мод для `svckit` — первый мод в ките и эталон формы для всех следующих (`redismod`, `natsmod`, `otelmod`, …). Оборачивает `clients/s3`; подключаете — платите за `aws-sdk-go-v2` в бинаре, не подключаете — не платите.

**Родитель:** [`svckit`](../../README.md)
**Импорт:** `github.com/theizzatbek/gokit/svckit/mods/s3mod`
**Цена импорта:** +7.26 MB (замерено `-ldflags="-s -w"`: `svckit.New` без модов — 20.38 MB, с `s3mod` — 27.64 MB; всё ещё на 11.40 MB / 29.2% меньше `service.New`, который тянет S3 всегда).

## Подключение

```go
type AppConfig struct {
    svckit.Config
    S3 s3mod.Config `envPrefix:"S3_"`
}

var cfg AppConfig
env.Parse(&cfg)

s3m := s3mod.New(cfg.S3)
svc, err := svckit.New[App, Claims](ctx, cfg.Config, s3m.Option())
if err != nil { return err }
defer svc.Close()

s3m.Client().Put(ctx, key, body)
```

`s3m.Option()` — единственный способ передать мод в `svckit.New`; под капотом это `svckit.WithMod(m)`.

## `Config` — алиас, не копия

```go
type Config = s3client.Config
```

Env-теги уже живут в `clients/s3.Config` — дублировать их здесь нет смысла. Композируйте конфиг мода в свой `AppConfig` через embedding с нужным `envPrefix`, как в примере выше. Для второго бакета — второй экземпляр мода с другим префиксом и `WithName`:

```go
primary := s3mod.New(cfg.S3)
backup  := s3mod.New(cfg.S3Backup, s3mod.WithName("s3-backup"))
svc, err := svckit.New[App, Claims](ctx, cfg.Config, primary.Option(), backup.Option())
```

`svckit.New` отвергает два мода с одинаковым `Name()` до единого сетевого вызова (`CodeModDuplicate`) — так что забытый `WithName` на втором экземпляре ловится на старте, а не на первом неоднозначном `Client()`.

## Пустой `Bucket` = мод выключен, не ошибка

`Build` проверяет `cfg.Bucket` сам, без участия ядра:

```go
func (m *Mod) Build(ctx context.Context, h svckit.Host) error {
    m.built = true
    if m.cfg.Bucket == "" {
        return nil // оператор не включил S3 — это не ошибка
    }
    ...
}
```

Мод остаётся живым объектом (`m.built == true`), просто без клиента внутри. Это отражается в трёх местах:

| Метод | Пустой `Bucket` | Настроенный `Bucket` |
|---|---|---|
| `Enabled() bool` | `false` | `true` |
| `Optional() (*s3client.Client, bool)` | `(nil, false)` | `(client, true)` |
| `Client() *s3client.Client` | паника, называющая `S3_BUCKET` | клиент |
| `Status().Mods[i].Enabled` | `false` (мод реализует `svckit.Enabler`) | `true` |
| `Status().Mods[i].Detail` | `nil` | `map[string]string{"bucket": ...}` |

## `Client()` — две причины паники, один текст на каждую

```go
func (m *Mod) Client() *s3client.Client {
    if !m.built {
        panic(...) // мод не передан в svckit.New через Option()
    }
    if m.client == nil {
        panic(...) // S3_BUCKET пуст
    }
    return m.client
}
```

Порядок проверок важен: `!m.built` ловится первым, потому что это программная ошибка (забыли `s3m.Option()` в списке опций `svckit.New`), а пустой `Bucket` — законная конфигурация оператора. Оба текста называют конкретную причину — не generic nil-deref в проде.

Для кода, который может работать и без S3, — `Optional()` вместо `Client()`:

```go
if cli, ok := s3m.Optional(); ok {
    cli.Put(ctx, key, body)
}
```

## Опции

- `WithName(name)` — переопределяет `Name()`; нужно для второго экземпляра мода в одном сервисе.
- `WithClientOptions(opts ...s3client.Option)` — форвардит в `s3client.Connect`. Logger и Metrics мод подключает сам из `Host`; сюда — retry policy, кастомный endpoint resolver и всё остальное, что принимает `clients/s3`.

## См. также

- [`clients/s3`](../../../clients/s3/README.md) — сам клиент: `aws-sdk-go-v2/service/s3`, AWS/MinIO/R2/Spaces/B2
- [`svckit`](../../README.md) — контракт `Mod`/`Host`, три фазы, `Status()`
