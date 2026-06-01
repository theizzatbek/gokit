# fibermap/fibermaptest

Тестовые хелперы для assertions над `Engine.Routes()` — проверяет, что ваш routes.yaml зарегистрировал правильные методы, пути, хендлеры, middleware и тэги. Естественно сочетается со snapshot-style "route inventory" тестами, чтобы деплой ловил отсутствующие или переименованные роуты.

**Родитель:** [../README.md](../README.md)
**Импорт:** `github.com/theizzatbek/gokit/fibermap/fibermaptest`

## Использование

```go
import (
    "testing"
    "github.com/theizzatbek/gokit/fibermap/fibermaptest"
)

func TestRoutes(t *testing.T) {
    eng := buildEngine(t)   // your wiring helper
    if err := eng.LoadFile("routes.yaml"); err != nil { t.Fatal(err) }

    fibermaptest.AssertRoute(t, eng, "GET", "/v1/ping",
        fibermaptest.WithHandler("ping.get"),
        fibermaptest.WithTags("health"),
    )

    fibermaptest.AssertRoute(t, eng, "POST", "/v1/tasks",
        fibermaptest.WithHandler("tasks.create"),
        fibermaptest.WithMiddleware("bearer", "require_role"),
    )

    fibermaptest.AssertNoRoute(t, eng, "DELETE", "/v1/admin")
    fibermaptest.AssertRouteCount(t, eng, 12)  // catch accidental adds/removes
}
```

## Заметки

- **`Engine.Routes()` — это source of truth.** Эти хелперы обходят этот список — они не гоняют HTTP-запросы. Для request-level тестов используйте `Engine.Mount(app)` + `app.Test(req)`.
- **`AssertRouteCount`** — это guard против молча добавленных роутов. Установите однажды через `len(eng.Routes())` и перезапускайте после намеренных изменений.
- **`WithMiddleware(names ...)` матчит как substring-set.** Список middleware роута ДОЛЖЕН СОДЕРЖАТЬ каждое переданное имя (order-agnostic, exact-name match). Используйте, чтобы поймать отсутствующий auth middleware на защищённом роуте.
- **`WithHandler(name)`** матчит зарегистрированное имя хендлера (из `RegisterHandler` + поле `handler:` в YAML).
- **Интерфейс `TB`** принимает `*testing.T`, `*testing.B` и всё остальное с `Errorf`/`Fatalf`/`Helper` — подходит и для бенчмарков.

## См. также

- [`fibermap`](../README.md) — `Engine.Routes()` — это то, над чем assertion'ит этот пакет
</content>
