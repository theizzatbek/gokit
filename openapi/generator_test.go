package openapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/fibermap"
	"github.com/theizzatbek/fibermap/openapi"
)

type appCtx struct {
	UserID, Role string
}

func buildEngine(t *testing.T, yaml string, register func(*fibermap.Engine[appCtx])) *fibermap.Engine[appCtx] {
	t.Helper()
	e := fibermap.New[appCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) { return appCtx{}, nil })
	if register != nil {
		register(e)
	}
	if err := e.LoadBytes([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	if err := e.Mount(fiber.New()); err != nil {
		t.Fatal(err)
	}
	return e
}

// decode unmarshals into a generic map so tests can poke into the JSON
// structure without needing the full Spec type.
func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, b)
	}
	return m
}

// path navigates nested maps. Returns nil if any segment doesn't
// resolve to a map.
func path(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

func TestGenerate_MinimalSkeleton(t *testing.T) {
	e := buildEngine(t, `
groups:
  - prefix: /v1
    routes:
      - method: GET
        path: /ping
        handler: ping
        name: ping
        description: Simple ping endpoint
        tags: [debug]
`, func(e *fibermap.Engine[appCtx]) {
		e.RegisterHandler("ping", func(c *fibermap.Context[appCtx]) error { return nil })
	})

	gen := openapi.NewGenerator(e,
		openapi.WithInfo(openapi.Info{Title: "Test API", Version: "1.2.3"}),
	)
	b, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc := decode(t, b)

	if doc["openapi"] != "3.0.3" {
		t.Errorf("openapi = %v, want 3.0.3", doc["openapi"])
	}
	if title := path(doc, "info", "title"); title != "Test API" {
		t.Errorf("info.title = %v", title)
	}
	op := path(doc, "paths", "/v1/ping", "get").(map[string]any)
	if op["operationId"] != "ping" {
		t.Errorf("operationId = %v", op["operationId"])
	}
	if op["description"] != "Simple ping endpoint" {
		t.Errorf("description = %v", op["description"])
	}
	tags := op["tags"].([]any)
	if len(tags) != 1 || tags[0] != "debug" {
		t.Errorf("tags = %v", tags)
	}
	// No body/query schemas declared → only the default 200 response.
	resp := op["responses"].(map[string]any)
	if _, ok := resp["200"]; !ok {
		t.Errorf("default 200 response missing: %v", resp)
	}
}

func TestGenerate_PathParamsTranslated(t *testing.T) {
	e := buildEngine(t, `
groups:
  - routes:
      - method: GET
        path: /users/:id/posts/:postId
        handler: posts.get
        name: posts.get
`, func(e *fibermap.Engine[appCtx]) {
		e.RegisterHandler("posts.get", func(c *fibermap.Context[appCtx]) error { return nil })
	})

	b, err := openapi.NewGenerator(e).Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc := decode(t, b)

	if path(doc, "paths", "/users/{id}/posts/{postId}") == nil {
		t.Errorf("expected /users/{id}/posts/{postId} path, paths = %v", doc["paths"])
	}
	op := path(doc, "paths", "/users/{id}/posts/{postId}", "get").(map[string]any)
	params := op["parameters"].([]any)
	if len(params) != 2 {
		t.Fatalf("expected 2 path params, got %d: %v", len(params), params)
	}
	gotNames := map[string]bool{}
	for _, p := range params {
		pm := p.(map[string]any)
		gotNames[pm["name"].(string)] = true
		if pm["in"] != "path" {
			t.Errorf("param %v in = %v, want path", pm["name"], pm["in"])
		}
		if pm["required"] != true {
			t.Errorf("param %v required = %v, want true", pm["name"], pm["required"])
		}
	}
	for _, want := range []string{"id", "postId"} {
		if !gotNames[want] {
			t.Errorf("missing path param %q", want)
		}
	}
}

type CreateTaskReq struct {
	Title    string `json:"title"             validate:"required,min=1,max=200"`
	Priority int    `json:"priority,omitempty" validate:"min=0,max=5"`
}

type TaskResponse struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func TestGenerate_HandlerSchemas(t *testing.T) {
	e := buildEngine(t, `
groups:
  - prefix: /api/v1
    routes:
      - method: POST
        path: /tasks
        handler: tasks.create
        name: tasks.create
        summary: Create a task
        tags: [tasks, write]
`, func(e *fibermap.Engine[appCtx]) {
		e.RegisterHandler("tasks.create", func(c *fibermap.Context[appCtx]) error { return nil })
	})

	gen := openapi.NewGenerator(e,
		openapi.WithInfo(openapi.Info{Title: "Tasks API", Version: "1.0.0"}),
	)
	gen.OnHandler("tasks.create").
		Body(CreateTaskReq{}).
		Response(201, TaskResponse{}).
		Response(400, ErrorResponse{})

	b, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc := decode(t, b)

	op := path(doc, "paths", "/api/v1/tasks", "post").(map[string]any)
	if op["summary"] != "Create a task" {
		t.Errorf("summary = %v", op["summary"])
	}

	// RequestBody → $ref into components/schemas.
	body := path(op, "requestBody", "content", "application/json", "schema").(map[string]any)
	ref, _ := body["$ref"].(string)
	if !strings.HasPrefix(ref, "#/components/schemas/") {
		t.Errorf("body $ref = %q, want '#/components/schemas/...'", ref)
	}

	// Response 201 has its own schema.
	resp201 := path(op, "responses", "201", "content", "application/json", "schema").(map[string]any)
	if _, ok := resp201["$ref"]; !ok {
		t.Errorf("response 201 missing $ref: %v", resp201)
	}

	// Components/schemas registry has both types.
	schemas := path(doc, "components", "schemas").(map[string]any)
	if _, ok := schemas["CreateTaskReq"]; !ok {
		t.Errorf("CreateTaskReq schema missing — schemas: %v", schemas)
	}
	if _, ok := schemas["TaskResponse"]; !ok {
		t.Errorf("TaskResponse schema missing — schemas: %v", schemas)
	}

	// CreateTaskReq honours `validate:"required"` → required array.
	createSchema := schemas["CreateTaskReq"].(map[string]any)
	required, _ := createSchema["required"].([]any)
	if len(required) == 0 || required[0] != "title" {
		t.Errorf("CreateTaskReq.required = %v, want [title]", required)
	}
}

func TestGenerate_SecurityFromMiddleware(t *testing.T) {
	e := buildEngine(t, `
middleware_sets:
  protected: [auth]
groups:
  - prefix: /api/v1
    middleware_set: protected
    routes:
      - method: GET
        path: /me
        handler: me.get
        name: me.get
`, func(e *fibermap.Engine[appCtx]) {
		e.RegisterMiddleware("auth", func(c *fibermap.Context[appCtx]) error { return c.Next() })
		e.RegisterHandler("me.get", func(c *fibermap.Context[appCtx]) error { return nil })
	})

	gen := openapi.NewGenerator(e,
		openapi.WithSecurity("BearerAuth", openapi.HTTPBearer("JWT")),
		openapi.MapMiddlewareToSecurity("auth", "BearerAuth"),
	)
	b, err := gen.Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	doc := decode(t, b)

	// security on the operation
	op := path(doc, "paths", "/api/v1/me", "get").(map[string]any)
	sec := op["security"].([]any)
	if len(sec) != 1 {
		t.Fatalf("security = %v, want one entry", sec)
	}
	entry := sec[0].(map[string]any)
	if _, ok := entry["BearerAuth"]; !ok {
		t.Errorf("security entry missing BearerAuth: %v", entry)
	}

	// scheme declared under components
	scheme := path(doc, "components", "securitySchemes", "BearerAuth").(map[string]any)
	if scheme["type"] != "http" || scheme["scheme"] != "bearer" || scheme["bearerFormat"] != "JWT" {
		t.Errorf("scheme = %v", scheme)
	}
}

func TestGenerate_UnknownSecuritySchemeRefFails(t *testing.T) {
	e := buildEngine(t, `
groups:
  - routes:
      - { method: GET, path: /x, handler: x, name: x }
`, func(e *fibermap.Engine[appCtx]) {
		e.RegisterHandler("x", func(c *fibermap.Context[appCtx]) error { return nil })
	})

	gen := openapi.NewGenerator(e,
		openapi.MapMiddlewareToSecurity("auth", "Missing"),
	)
	_, err := gen.Generate()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Missing") {
		t.Errorf("err = %v, want mention of the unknown scheme name", err)
	}
}

func TestGenerate_DefaultsWhenNoOptions(t *testing.T) {
	e := buildEngine(t, `
groups:
  - routes:
      - { method: GET, path: /x, handler: x, name: x }
`, func(e *fibermap.Engine[appCtx]) {
		e.RegisterHandler("x", func(c *fibermap.Context[appCtx]) error { return nil })
	})

	b, err := openapi.NewGenerator(e).Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc := decode(t, b)

	info := doc["info"].(map[string]any)
	if info["title"] != "fibermap API" {
		t.Errorf("default title = %v", info["title"])
	}
	if info["version"] != "0.0.0" {
		t.Errorf("default version = %v", info["version"])
	}
}
