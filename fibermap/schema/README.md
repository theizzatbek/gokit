# fibermap/schema

JSON Schema (Draft 2020-12) для `routes.yaml`. Встроен в бинарь во время компиляции и доступен через top-level хелпер `fibermap.Schema() []byte`. Питает editor-автодополнение + диагностику в любом YAML language server'е, который поддерживает `# yaml-language-server: $schema=...`.

**Родитель:** [../README.md](../README.md)
**Файлы:** `routes.schema.json` (единственный файл)
**Импорт:** нет (data-only пакет — доступ через `fibermap.Schema()`)

## Использование

### Настройка редактора

Добавьте эту строку в начало любого `routes.yaml`:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/theizzatbek/gokit/main/fibermap/schema/routes.schema.json
```

VS Code (с [redhat.vscode-yaml]), GoLand и Vim с `coc-yaml` дают:

- автодополнение для `method` / `middleware_sets` / `cache.ttl` / и т.д.
- hover-документацию на каждом поле
- inline-диагностику для опечаток в `middleware:` ссылках и несоответствий формы

### Программный доступ

```go
import "github.com/theizzatbek/gokit/fibermap"

raw := fibermap.Schema()   // []byte JSON-схемы
// Используйте для валидации routes.yaml в CI без зависимости от gokit-бинаря.
```

Автономный CLI печатает те же байты:

```bash
fibermap dump-schema > routes.schema.json
```

## Заметки

- **Схема покрывает форму, не семантику.** Вещи вроде "handler `tasks.create` действительно существует" НЕ проверяются — они проверяются на стадии `Engine.Mount`, когда регистрации встречаются с YAML.
- **Обновления приезжают с бинарём.** Редакторы, которые тянут схему с GitHub raw, получают версию, упакованную с tagged release; запиньте специфичный тэг в URL, если хотите стабильности в churn'е `main`.
- **URL схемы изменился после rebrand'а** — старые `raw.githubusercontent.com/theizzatbek/fibermap/...` redirect'ы через переименование НЕ гарантируются; обновите modeline в вашем `routes.yaml` на новый путь.

[redhat.vscode-yaml]: https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml

## См. также

- [`fibermap`](../README.md) — accessor `Schema()`, `Engine.LoadFile()` — это то, что в runtime потребляет routes.yaml
- `cmd/fibermap` — CLI для `validate routes.yaml` + `dump-schema`
</content>
