package fibermap

import (
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/gokit/fibermap/bind"
)

// Tests for v1.1.0 P1-4: fibermap.ErrsvalBindError as the recommended
// bind-error handler. Two angles:
//
//   1. Pure unit on bindSourceCode — fast, no fiber.App. Covers the
//      source → code mapping.
//   2. Round-trip through fiber.App + the real validator.v10 + the
//      bind.Body path — proves that errsval.FromValidator extracts
//      per-field Details[] from the wrapped chain (which only works
//      because v1.0.1 P0-1 switched bind from fmt.Errorf("%w: %v")
//      to errors.Join, preserving validator.ValidationErrors via
//      errors.As).

func TestBindSourceCode_MapsEverySource(t *testing.T) {
	cases := map[error]string{
		bind.ErrParseBody:      "invalid_body",
		bind.ErrValidateBody:   "invalid_body",
		bind.ErrParseQuery:     "invalid_query",
		bind.ErrValidateQuery:  "invalid_query",
		bind.ErrParseParams:    "invalid_params",
		bind.ErrValidateParams: "invalid_params",
		bind.ErrParseHeader:    "invalid_header",
		bind.ErrValidateHeader: "invalid_header",
		errors.New("some random error not in the bind family"): "invalid_request",
	}
	for sentinel, want := range cases {
		t.Run(want+"::"+sentinel.Error(), func(t *testing.T) {
			if got := bindSourceCode(sentinel); got != want {
				t.Errorf("bindSourceCode(%v) = %q, want %q", sentinel, got, want)
			}
		})
	}
}

func TestBindSourceCode_WalksJoinChain(t *testing.T) {
	// bind wraps the inner err via errors.Join — bindSourceCode must
	// walk the chain via errors.Is, not just inspect the top-level
	// error.
	wrapped := errors.Join(bind.ErrValidateBody, errors.New("inner detail"))
	if got := bindSourceCode(wrapped); got != "invalid_body" {
		t.Errorf("bindSourceCode(joined chain) = %q, want invalid_body", got)
	}
}

type errsvalCtx struct{ UserID string }

type errsvalBody struct {
	Email string `json:"email" validate:"required,email"`
	Age   int    `json:"age"   validate:"required,min=18"`
}

type errsvalQuery struct {
	Limit int `query:"limit" validate:"required,min=1,max=100"`
}

type errsvalParams struct {
	ID string `params:"id" validate:"required,uuid"`
}

type errsvalHeader struct {
	Token string `reqHeader:"X-Token" validate:"required,min=10"`
}

func mountWithErrsval(t *testing.T, yaml string, wire func(*Engine[errsvalCtx])) *fiber.App {
	t.Helper()
	e := New[errsvalCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (errsvalCtx, error) { return errsvalCtx{}, nil })
	e.SetValidator(validator.New(validator.WithRequiredStructEnabled()))
	e.SetBindErrorHandler(ErrsvalBindError[errsvalCtx])
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

func TestErrsvalBindError_BodyValidationFail_400_TypedJSON(t *testing.T) {
	app := mountWithErrsval(t,
		`groups: [{routes: [{method: POST, path: /u, handler: u.create}]}]`,
		func(e *Engine[errsvalCtx]) {
			RegisterHandlerWithBody(e, "u.create", func(c *Context[errsvalCtx], req errsvalBody) error {
				t.Errorf("handler should not run on validation fail; got req=%+v", req)
				return nil
			})
		})

	req := httptest.NewRequest("POST", "/u", strings.NewReader(`{"email":"not-an-email","age":12}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (raw: %s)", err, body)
	}

	if out["code"] != "invalid_body" {
		t.Errorf("code = %v, want invalid_body", out["code"])
	}
	if _, ok := out["message"].(string); !ok {
		t.Errorf("message missing or not string: %v", out["message"])
	}
	details, ok := out["details"].([]any)
	if !ok {
		t.Fatalf("details missing or not []any: %v", out["details"])
	}
	if len(details) < 2 {
		t.Errorf("expected ≥ 2 field errors (email + age), got %d: %v", len(details), details)
	}
	// Spot-check shape of one entry.
	first, ok := details[0].(map[string]any)
	if !ok {
		t.Fatalf("details[0] not map: %v", details[0])
	}
	if _, ok := first["field"].(string); !ok {
		t.Errorf("details[0].field missing or not string: %v", first)
	}
	if _, ok := first["rule"].(string); !ok {
		t.Errorf("details[0].rule missing or not string: %v", first)
	}
}

func TestErrsvalBindError_BodyParseFail_400_InvalidBody(t *testing.T) {
	// Garbage JSON triggers bind.ErrParseBody, NOT a validator failure.
	// errsval.FromValidator returns the err unchanged; the helper falls
	// back to a single-field xerrs.Validation with the err.Error()
	// message and the source code "invalid_body".
	app := mountWithErrsval(t,
		`groups: [{routes: [{method: POST, path: /u, handler: u.create}]}]`,
		func(e *Engine[errsvalCtx]) {
			RegisterHandlerWithBody(e, "u.create", func(c *Context[errsvalCtx], req errsvalBody) error {
				t.Errorf("handler should not run on parse fail; got req=%+v", req)
				return nil
			})
		})

	req := httptest.NewRequest("POST", "/u", strings.NewReader(`{"email": not valid json`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	if out["code"] != "invalid_body" {
		t.Errorf("code = %v, want invalid_body", out["code"])
	}
	if _, hasDetails := out["details"]; hasDetails {
		// Parse-stage errors don't carry per-field details — no
		// validator chain to walk. The kit wire shape omits details
		// entirely when empty (per errs.Response omitempty contract).
		// If errs ever changes that contract, this assertion will catch
		// the surprise.
		if d, _ := out["details"].([]any); len(d) != 0 {
			t.Errorf("parse-stage error should not have details; got %v", out["details"])
		}
	}
}

func TestErrsvalBindError_QueryValidationFail_400_InvalidQuery(t *testing.T) {
	app := mountWithErrsval(t,
		`groups: [{routes: [{method: GET, path: /u, handler: u.list}]}]`,
		func(e *Engine[errsvalCtx]) {
			RegisterHandlerWithQuery(e, "u.list", func(c *Context[errsvalCtx], q errsvalQuery) error {
				t.Errorf("handler should not run; got q=%+v", q)
				return nil
			})
		})

	req := httptest.NewRequest("GET", "/u?limit=999", nil)
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	if out["code"] != "invalid_query" {
		t.Errorf("code = %v, want invalid_query", out["code"])
	}
}

func TestErrsvalBindError_ParamsValidationFail_400_InvalidParams(t *testing.T) {
	app := mountWithErrsval(t,
		`groups: [{routes: [{method: GET, path: /u/:id, handler: u.get}]}]`,
		func(e *Engine[errsvalCtx]) {
			RegisterHandlerWithParams(e, "u.get", func(c *Context[errsvalCtx], p errsvalParams) error {
				t.Errorf("handler should not run; got p=%+v", p)
				return nil
			})
		})

	req := httptest.NewRequest("GET", "/u/not-a-uuid", nil)
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	if out["code"] != "invalid_params" {
		t.Errorf("code = %v, want invalid_params", out["code"])
	}
}

func TestErrsvalBindError_HeaderValidationFail_400_InvalidHeader(t *testing.T) {
	app := mountWithErrsval(t,
		`groups: [{routes: [{method: GET, path: /u, handler: u.h}]}]`,
		func(e *Engine[errsvalCtx]) {
			RegisterHandlerWithHeaders(e, "u.h", func(c *Context[errsvalCtx], h errsvalHeader) error {
				t.Errorf("handler should not run; got h=%+v", h)
				return nil
			})
		})

	req := httptest.NewRequest("GET", "/u", nil)
	// missing X-Token header → validate fail
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	if out["code"] != "invalid_header" {
		t.Errorf("code = %v, want invalid_header", out["code"])
	}
}

func TestErrsvalBindError_NilError_NilReturn(t *testing.T) {
	// Direct unit test of the helper's nil-fast-path; skips the
	// round-trip overhead.
	app := fiber.New()
	defer app.Shutdown()
	// Construct a fibermap.Context that wraps a real fiber.Ctx so
	// SkipFiberInternal stays honest. We use Test to round-trip a
	// trivial handler that calls ErrsvalBindError(c, nil) and
	// observes the response is unchanged.
	app.Get("/", func(c *fiber.Ctx) error {
		// Manually build a Context[any] mirror of what fibermap does
		// at install time so the helper sees a plausible struct.
		fc := &Context[errsvalCtx]{Ctx: c, Data: errsvalCtx{}}
		if err := ErrsvalBindError[errsvalCtx](fc, nil); err != nil {
			t.Errorf("ErrsvalBindError(c, nil) = %v, want nil", err)
		}
		return c.SendStatus(200)
	})

	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != 200 {
		t.Errorf("nil-fast-path leaked through; status = %d", resp.StatusCode)
	}
}
