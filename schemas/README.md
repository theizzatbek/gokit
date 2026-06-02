# schemas

Этот пакет хранит JSON Schema (draft-07) для всех YAML-конфигов кита и
встраивает их в бинарь через `//go:embed`. Файлы используются в двух
ролях:

1. **IDE-валидация YAML.** `yaml-language-server` (VS Code, GoLand,
   Vim/coc-yaml) подхватывает modeline в начале YAML-файла и даёт
   автодополнение, hover-документацию и inline-диагностику ещё до
   запуска `go test`.
2. **Runtime-доступ из Go.** Каждая схема экспортируется как
   `func() []byte` — приложение может отдать её из `dump-schema` CLI,
   из admin-эндпоинта, или прогнать через JSON-валидатор.

## Карта схем

| Файл                     | Подсистема           | Покрывает                        |
|--------------------------|----------------------|----------------------------------|
| `routes.schema.json`     | `fibermap`           | `routes.yaml`                    |
| `crons.schema.json`      | `cronmap`            | `crons.yaml`                     |
| `clients.schema.json`    | `clients/apimap`     | `clients.yaml`                   |
| `natsmap.schema.json`    | `clients/natsmap`    | `subscribers.yaml`, `publishers.yaml` (combined union) |

## IDE wiring

Добавьте modeline первой строкой в соответствующий YAML:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/theizzatbek/gokit/main/schemas/routes.schema.json
```

Полный набор:

```yaml
# routes.yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/theizzatbek/gokit/main/schemas/routes.schema.json

# crons.yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/theizzatbek/gokit/main/schemas/crons.schema.json

# clients.yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/theizzatbek/gokit/main/schemas/clients.schema.json

# subscribers.yaml / publishers.yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/theizzatbek/gokit/main/schemas/natsmap.schema.json
```

`natsmap.schema.json` намеренно описывает union `subscribers` /
`publishers` / `streams` верхнего уровня — один и тот же файл схемы
валидирует и комбинированный YAML, и отдельные `subscribers.yaml` /
`publishers.yaml`.

### VS Code (без modeline)

Если modeline не подходит (например, YAML генерируется), пропишите
ассоциацию в `.vscode/settings.json`:

```json
{
  "yaml.schemas": {
    "https://raw.githubusercontent.com/theizzatbek/gokit/main/schemas/routes.schema.json": "routes.yaml",
    "https://raw.githubusercontent.com/theizzatbek/gokit/main/schemas/crons.schema.json": "crons.yaml",
    "https://raw.githubusercontent.com/theizzatbek/gokit/main/schemas/clients.schema.json": "clients.yaml",
    "https://raw.githubusercontent.com/theizzatbek/gokit/main/schemas/natsmap.schema.json": [
      "subscribers.yaml",
      "publishers.yaml"
    ]
  }
}
```

## Доступ из Go

```go
import "github.com/theizzatbek/gokit/schemas"

raw := schemas.Routes()  // []byte JSON Schema for routes.yaml
_ = schemas.Crons()
_ = schemas.Clients()
_ = schemas.NATSMap()
```

`fibermap.Schema()` остаётся как удобный per-package alias и просто
делегирует в `schemas.Routes()`.

## Когда обновлять схемы

При добавлении нового поля в любой `raw*` YAML-структуру (например, в
`fibermap/spec.go`, `cronmap/spec.go`, `clients/apimap/spec.go`,
`clients/natsmap/spec.go`) — обновите соответствующий JSON-файл
здесь. Схемы намеренно покрывают **обязательные поля + enum'ы**, а не
полную поверхность: цель — поймать опечатки в редакторе, а не
дублировать Go-валидацию `Build`/`Mount`.
