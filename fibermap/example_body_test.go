package fibermap_test

import (
	"fmt"
	"io"
	"net/http/httptest"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/gokit/fibermap"
)

// ExampleRegisterHandlerWithBody shows the typed body-bound handler: the
// request JSON is decoded (and, when a validator is set via
// SetValidator, validated) into Req before the handler runs, collapsing
// the usual parse boilerplate into a single typed argument.
func ExampleRegisterHandlerWithBody() {
	type CreateUser struct {
		Name string `json:"name" validate:"required"`
	}

	eng := fibermap.New[docCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (docCtx, error) { return docCtx{}, nil })

	fibermap.RegisterHandlerWithBody(eng, "users.create",
		func(c *docCtxRef, req CreateUser) error {
			return c.Status(fiber.StatusCreated).JSON(fiber.Map{"created": req.Name})
		})

	if err := eng.LoadBytes([]byte(`
groups:
  - prefix: /v1
    routes:
      - { method: POST, path: /users, handler: users.create }
`)); err != nil {
		panic(err)
	}

	app := fiber.New()
	if err := eng.Mount(app); err != nil {
		panic(err)
	}

	req := httptest.NewRequest("POST", "/v1/users", strings.NewReader(`{"name":"Ada"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		panic(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	fmt.Println(resp.StatusCode)
	fmt.Println(string(body))
	// Output:
	// 201
	// {"created":"Ada"}
}
