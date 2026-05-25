# fibermap/schema

JSON Schema (Draft 2020-12) for `routes.yaml`. Embedded into the binary at compile time and exposed via the top-level helper `fibermap.Schema() []byte`. Powers editor autocomplete + diagnostics in any YAML language server that supports `# yaml-language-server: $schema=...`.

**Parent:** [../README.md](../README.md)
**Files:** `routes.schema.json` (the single file)
**Import:** none (data-only package — accessed via `fibermap.Schema()`)

## Use

### Editor setup

Add this line at the top of any `routes.yaml`:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/theizzatbek/gokit/main/fibermap/schema/routes.schema.json
```

VS Code (with [redhat.vscode-yaml]), GoLand, and Vim with `coc-yaml` give:

- autocomplete for `method` / `middleware_sets` / `cache.ttl` / etc.
- hover docs on every field
- inline diagnostics for typos in `middleware:` references and shape mismatches

### Programmatic access

```go
import "github.com/theizzatbek/gokit/fibermap"

raw := fibermap.Schema()   // []byte of the JSON schema
// Use to validate routes.yaml in CI without depending on the gokit binary.
```

The standalone CLI prints the same bytes:

```bash
fibermap dump-schema > routes.schema.json
```

## Notes

- **The schema covers shape, not semantics.** Things like "handler `tasks.create` actually exists" are NOT enforced — they're checked at `Engine.Mount` time when registrations + YAML meet.
- **Updates land with the binary.** Editors that fetch the schema from GitHub raw get the schema bundled with the tagged release; pin a specific tag in the URL if you want stability across `main` churn.
- **Schema URL changed on rebrand** — old `raw.githubusercontent.com/theizzatbek/fibermap/...` redirects via the rename are NOT guaranteed; update your `routes.yaml` modeline to the new path.

[redhat.vscode-yaml]: https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml

## See also

- [`fibermap`](../README.md) — `Schema()` accessor, `Engine.LoadFile()` is what consumes routes.yaml at runtime
- `cmd/fibermap` — CLI for `validate routes.yaml` + `dump-schema`
