package fibermap

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// Combined-source input types used by RegisterHandlerWithInput tests.
type updateBody struct {
	Title string `json:"title"`
}
type updateInputBodyParams struct {
	Body   updateBody
	Params idParams
}
type updateInputBodyQuery struct {
	Body  updateBody
	Query listQuery
}
type updateInputAll struct {
	Body    updateBody
	Params  idParams
	Query   listQuery
	Headers authHeader
}
type emptyInput struct {
	NotRecognised string `json:"not_recognised"`
}
type badFieldInput struct {
	Body string // not a struct
}

// strictValidator already handles createReq, listQuery, idParams,
// authHeader. Extend it for updateBody used here.
type strictValidatorWithBody struct{ strictValidator }

func (strictValidatorWithBody) Struct(s any) error {
	if b, ok := s.(*updateBody); ok && b.Title == "" {
		return errors.New("title required")
	}
	return strictValidator{}.Struct(s)
}

func TestRegisterHandlerWithInput_BodyAndParams(t *testing.T) {
	app := mountFor(t, `groups:
  - routes:
      - {method: PUT, path: /tasks/:id, handler: tasks.update, name: tasks.update}
`, func(e *Engine[registerCtx]) {
		RegisterHandlerWithInput(e, "tasks.update",
			func(c *Context[registerCtx], in updateInputBodyParams) error {
				return c.SendString("updated:" + in.Params.ID + ":" + in.Body.Title)
			})
	})

	req := httptest.NewRequest("PUT", "/tasks/abc-123",
		strings.NewReader(`{"title":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "updated:abc-123:hello" {
		t.Errorf("body = %q", string(body))
	}
}

func TestRegisterHandlerWithInput_BodyAndQuery(t *testing.T) {
	app := mountFor(t, `groups:
  - routes:
      - {method: POST, path: /search, handler: search, name: search}
`, func(e *Engine[registerCtx]) {
		RegisterHandlerWithInput(e, "search",
			func(c *Context[registerCtx], in updateInputBodyQuery) error {
				return c.SendString("limit:" + itoa(in.Query.Limit) + ":title:" + in.Body.Title)
			})
	})

	req := httptest.NewRequest("POST", "/search?limit=5",
		strings.NewReader(`{"title":"abc"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "limit:5:title:abc" {
		t.Errorf("body = %q", string(body))
	}
}

func TestRegisterHandlerWithInput_AllFour(t *testing.T) {
	app := mountFor(t, `groups:
  - routes:
      - {method: PATCH, path: /things/:id, handler: things.patch, name: things.patch}
`, func(e *Engine[registerCtx]) {
		RegisterHandlerWithInput(e, "things.patch",
			func(c *Context[registerCtx], in updateInputAll) error {
				return c.SendString(in.Params.ID + ":" + in.Body.Title +
					":" + itoa(in.Query.Limit) + ":" + in.Headers.Authorization)
			})
	})

	req := httptest.NewRequest("PATCH", "/things/x42?limit=9",
		strings.NewReader(`{"title":"go"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer t")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "x42:go:9:Bearer t" {
		t.Errorf("body = %q", string(body))
	}
}

func TestRegisterHandlerWithInput_ValidatorRunsPerField(t *testing.T) {
	// Custom validator rejects empty Title — should cause 400 even though
	// the body parsed fine.
	e := New[registerCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (registerCtx, error) { return registerCtx{}, nil })
	e.SetValidator(strictValidatorWithBody{})

	RegisterHandlerWithInput(e, "tasks.update",
		func(c *Context[registerCtx], in updateInputBodyParams) error {
			return c.SendString("ok")
		})
	if err := e.LoadBytes([]byte(`groups:
  - routes:
      - {method: PUT, path: /tasks/:id, handler: tasks.update, name: tasks.update}
`)); err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(nil)})
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	// Empty title — validator rejects.
	req := httptest.NewRequest("PUT", "/tasks/abc",
		strings.NewReader(`{"title":""}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Errorf("empty title status = %d, want 400", resp.StatusCode)
	}
}

func TestRegisterHandlerWithInput_AttachesOpenAPIMeta(t *testing.T) {
	e := New[registerCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (registerCtx, error) { return registerCtx{}, nil })
	RegisterHandlerWithInput(e, "tasks.update",
		func(c *Context[registerCtx], in updateInputBodyParams) error { return nil })

	// HandlerMeta should reflect all recognised fields.
	meta := e.HandlerMeta("tasks.update")
	if meta == nil {
		t.Fatal("HandlerMeta nil — auto-attach did not run")
	}
	if meta.Body == nil {
		t.Errorf("meta.Body is nil")
	}
	if meta.Params == nil {
		t.Errorf("meta.Params is nil")
	}
	if meta.Query != nil {
		t.Errorf("meta.Query should be nil for body+params input, got %v", meta.Query)
	}
}

func TestRegisterHandlerWithInput_PanicNoRecognisedFields(t *testing.T) {
	e := New[registerCtx]()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic, got none")
		}
		fe, ok := r.(*Error)
		if !ok {
			t.Fatalf("recover returned %T, want *Error", r)
		}
		if fe.Code != CodeRegisterMisuse {
			t.Errorf("code = %q, want %q", fe.Code, CodeRegisterMisuse)
		}
	}()
	RegisterHandlerWithInput(e, "x",
		func(c *Context[registerCtx], in emptyInput) error { return nil })
}

func TestRegisterHandlerWithInput_PanicFieldNotStruct(t *testing.T) {
	e := New[registerCtx]()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic, got none")
		}
		fe, ok := r.(*Error)
		if !ok {
			t.Fatalf("recover returned %T, want *Error", r)
		}
		if fe.Code != CodeRegisterMisuse {
			t.Errorf("code = %q, want %q", fe.Code, CodeRegisterMisuse)
		}
	}()
	RegisterHandlerWithInput(e, "x",
		func(c *Context[registerCtx], in badFieldInput) error { return nil })
}
