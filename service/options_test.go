package service

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/gokit/fibermap"
)

func TestWithAPIMap_FlipsAPIMapEnable(t *testing.T) {
	o := &options{}
	WithAPIMap()(o)
	if !o.apimapEnable {
		t.Fatal("WithAPIMap() did not flip apimapEnable")
	}
}

func TestWithNATSMap_FlipsNATSMapEnable(t *testing.T) {
	o := &options{}
	WithNATSMap()(o)
	if !o.natsmapEnable {
		t.Fatal("WithNATSMap() did not flip natsmapEnable")
	}
}

func TestWithRoutes_FlipsRoutesEnable(t *testing.T) {
	o := &options{}
	WithRoutes()(o)
	if !o.routesEnable {
		t.Fatal("WithRoutes() did not flip routesEnable")
	}
}

// recordingValidator counts Struct calls and returns the canned error.
type recordingValidator struct {
	calls int
	err   error
}

func (r *recordingValidator) Struct(any) error {
	r.calls++
	return r.err
}

func TestWithValidator_StoresValidator(t *testing.T) {
	o := &options{}
	rv := &recordingValidator{}
	WithValidator(rv)(o)
	if o.validator == nil {
		t.Fatal("WithValidator did not store the validator")
	}
	if o.validator != rv {
		t.Errorf("WithValidator stored %v, want %v", o.validator, rv)
	}
}

type validatorTestCtx struct{}
type validatorTestBody struct {
	Name string `json:"name" validate:"required"`
}

// TestBuildEngine_CustomValidatorRunsOnBind exercises the wiring end-to-end:
// service.New + WithValidator → buildEngine → SetValidator → bind.Body
// during a request hits the user's validator.
func TestBuildEngine_CustomValidatorRunsOnBind(t *testing.T) {
	rv := &recordingValidator{err: errors.New("custom-rejected")}
	s := &Service[validatorTestCtx, struct{}]{opts: &options{validator: rv}}
	if err := s.buildEngine(); err != nil {
		t.Fatal(err)
	}
	s.Engine.SetContextBuilder(func(c *fiber.Ctx) (validatorTestCtx, error) {
		return validatorTestCtx{}, nil
	})

	fibermap.RegisterHandlerWithBody(s.Engine, "test.create",
		func(c *fibermap.Context[validatorTestCtx], body validatorTestBody) error {
			return c.SendStatus(204)
		})
	if err := s.Engine.LoadBytes([]byte(`
groups:
  - routes:
      - {method: POST, path: /v, handler: test.create, name: test.create}
`)); err != nil {
		t.Fatal(err)
	}

	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	if err := s.Engine.Mount(app); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/v", strings.NewReader(`{"name":"alice"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if rv.calls == 0 {
		t.Fatalf("recordingValidator was not called — WithValidator did not propagate (status=%d)", resp.StatusCode)
	}
	// Since rv returns an error, the bind path must surface it as 400.
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (validator returned error)", resp.StatusCode)
	}
}

func TestBuildEngine_DefaultsValidatorWhenNil(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{}}
	if err := s.buildEngine(); err != nil {
		t.Fatal(err)
	}
	if s.Engine == nil {
		t.Fatal("buildEngine did not populate Engine")
	}
}
