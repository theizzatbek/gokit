package fibermap

import (
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/gokit/fibermap/bind"
)

type registerCtx struct{ UserID string }

// strictValidator fails any struct with a Title field whose value
// is empty — enough to prove the validate step runs.
type strictValidator struct{}

func (strictValidator) Struct(s any) error {
	// Reflect-free shim: only used for the two test struct types below.
	switch v := s.(type) {
	case *createReq:
		if v.Title == "" {
			return errors.New("Title is required")
		}
	case *listQuery:
		if v.Limit <= 0 {
			return errors.New("Limit must be > 0")
		}
	}
	return nil
}

type createReq struct {
	Title string `json:"title"`
}

type listQuery struct {
	Limit int `query:"limit"`
}

type idParams struct {
	ID string `params:"id"`
}

type authHeader struct {
	Authorization string `reqHeader:"Authorization"`
}

// mountWithHandlers wires an engine with the given route and registers
// helpers. Returns the running fiber.App.
func mountFor(t *testing.T, yaml string, wire func(*Engine[registerCtx])) *fiber.App {
	t.Helper()
	e := New[registerCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (registerCtx, error) { return registerCtx{}, nil })
	e.SetValidator(strictValidator{})
	wire(e)
	if err := e.LoadBytes([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestRegisterBody_HappyPath(t *testing.T) {
	app := mountFor(t,
		`groups: [{routes: [{method: POST, path: /tasks, handler: tasks.create}]}]`,
		func(e *Engine[registerCtx]) {
			RegisterHandlerWithBody(e, "tasks.create", func(c *Context[registerCtx], req createReq) error {
				return c.SendString("got: " + req.Title)
			})
		},
	)
	req := httptest.NewRequest("POST", "/tasks", strings.NewReader(`{"title":"buy milk"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "got: buy milk" {
		t.Errorf("status=%d body=%q", resp.StatusCode, string(body))
	}
}

func TestRegisterBody_ValidationFail_400(t *testing.T) {
	app := mountFor(t,
		`groups: [{routes: [{method: POST, path: /tasks, handler: tasks.create}]}]`,
		func(e *Engine[registerCtx]) {
			RegisterHandlerWithBody(e, "tasks.create", func(c *Context[registerCtx], req createReq) error {
				t.Errorf("handler should not run on validation fail; got req=%+v", req)
				return nil
			})
		},
	)
	req := httptest.NewRequest("POST", "/tasks", strings.NewReader(`{"title":""}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out map[string]string
	_ = json.Unmarshal(body, &out)
	if !strings.Contains(out["error"], "validate") {
		t.Errorf("body = %v, want a validate-flavoured error", out)
	}
}

func TestRegisterBody_ParseFail_400(t *testing.T) {
	app := mountFor(t,
		`groups: [{routes: [{method: POST, path: /tasks, handler: tasks.create}]}]`,
		func(e *Engine[registerCtx]) {
			RegisterHandlerWithBody(e, "tasks.create", func(c *Context[registerCtx], req createReq) error {
				return nil
			})
		},
	)
	req := httptest.NewRequest("POST", "/tasks", strings.NewReader(`{not json`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRegisterBody_CustomBindErrorHandler(t *testing.T) {
	app := mountFor(t,
		`groups: [{routes: [{method: POST, path: /tasks, handler: tasks.create}]}]`,
		func(e *Engine[registerCtx]) {
			e.SetBindErrorHandler(func(c *Context[registerCtx], err error) error {
				kind := "unknown"
				switch {
				case errors.Is(err, bind.ErrParseBody):
					kind = "parse"
				case errors.Is(err, bind.ErrValidateBody):
					kind = "validate"
				}
				return c.Status(422).JSON(map[string]string{"kind": kind})
			})
			RegisterHandlerWithBody(e, "tasks.create", func(c *Context[registerCtx], req createReq) error {
				return nil
			})
		},
	)
	req := httptest.NewRequest("POST", "/tasks", strings.NewReader(`{"title":""}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != 422 {
		t.Errorf("status = %d, want 422 (custom handler ran)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"kind":"validate"`) {
		t.Errorf("body = %s, want kind=validate", string(body))
	}
}

func TestRegisterBody_AutoAttachesSchema(t *testing.T) {
	e := New[registerCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (registerCtx, error) { return registerCtx{}, nil })
	RegisterHandlerWithBody(e, "tasks.create", func(c *Context[registerCtx], req createReq) error { return nil })
	meta := e.HandlerMeta("tasks.create")
	if meta == nil {
		t.Fatal("HandlerMeta missing — RegisterHandlerWithBody didn't attach the schema")
	}
	if _, ok := meta.Body.(createReq); !ok {
		t.Errorf("meta.Body = %T, want createReq", meta.Body)
	}
}

func TestRegisterQuery_AutoBinds(t *testing.T) {
	app := mountFor(t,
		`groups: [{routes: [{method: GET, path: /things, handler: things.list}]}]`,
		func(e *Engine[registerCtx]) {
			RegisterHandlerWithQuery(e, "things.list", func(c *Context[registerCtx], q listQuery) error {
				return c.SendString("limit: " + itoa(q.Limit))
			})
		},
	)
	resp, _ := app.Test(httptest.NewRequest("GET", "/things?limit=42", nil))
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "limit: 42" {
		t.Errorf("body = %q", string(body))
	}
}

func TestRegisterParams_AutoBinds(t *testing.T) {
	app := mountFor(t,
		`groups: [{routes: [{method: GET, path: /things/:id, handler: things.get}]}]`,
		func(e *Engine[registerCtx]) {
			RegisterHandlerWithParams(e, "things.get", func(c *Context[registerCtx], p idParams) error {
				return c.SendString("id: " + p.ID)
			})
		},
	)
	resp, _ := app.Test(httptest.NewRequest("GET", "/things/abc-123", nil))
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "id: abc-123" {
		t.Errorf("body = %q", string(body))
	}
}

func TestRegisterHeaders_AutoBinds(t *testing.T) {
	app := mountFor(t,
		`groups: [{routes: [{method: GET, path: /me, handler: me.get}]}]`,
		func(e *Engine[registerCtx]) {
			RegisterHandlerWithHeaders(e, "me.get", func(c *Context[registerCtx], h authHeader) error {
				return c.SendString("auth: " + h.Authorization)
			})
		},
	)
	req := httptest.NewRequest("GET", "/me", nil)
	req.Header.Set("Authorization", "Bearer xyz")
	resp, _ := app.Test(req)
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "auth: Bearer xyz" {
		t.Errorf("body = %q", string(body))
	}
}

// itoa avoids importing strconv just for the tests above.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
