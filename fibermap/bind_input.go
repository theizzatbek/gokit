package fibermap

import (
	"fmt"
	"reflect"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/fibermap/bind"
)

// RegisterHandlerWithInput is the multi-source binder registrar. Use
// it when one endpoint needs more than one of {body, route params,
// query, headers} typed and validated — i.e. cases the single-source
// RegisterHandlerWith{Body,Params,Query,Headers} variants don't cover.
//
// Input must be a struct whose fields are themselves structs, named
// (case-sensitive) from the reserved set:
//
//	Body    — parsed via Fiber's BodyParser   (json/form/multipart tags)
//	Params  — parsed via Fiber's ParamsParser (`params:` tag)
//	Query   — parsed via Fiber's QueryParser  (`query:` tag)
//	Headers — parsed via Fiber's ReqHeaderParser (`reqHeader:` tag)
//
// Any combination is allowed; fields with other names are ignored.
// Example:
//
//	type UpdateInput struct {
//	    Body   UpdateBody     // {"title":..., "tags":[...]}
//	    Params struct {       // /tasks/:id
//	        ID string `params:"id" validate:"required,uuid"`
//	    }
//	    Query struct {        // ?notify=true
//	        Notify bool `query:"notify"`
//	    }
//	}
//
//	fibermap.RegisterHandlerWithInput(eng, "tasks.update",
//	    func(c *fibermap.Context[T], in UpdateInput) error {
//	        // in.Body, in.Params, in.Query already parsed + validated.
//	    })
//
// Validation uses the engine's validator (SetValidator) for every
// recognised field, mirroring the single-source registrars. Bind /
// validation errors route through Engine.BindErrorFunc (same default
// 400 JSON, same SetBindErrorHandler hook).
//
// Each recognised field also auto-attaches the matching HandlerOption
// (WithBody / WithParams / WithQuery / WithHeaders) so OpenAPI
// generation sees the full schema set without the caller threading
// any opts.
//
// Panics with *Error{CodeRegisterMisuse} on misuse: Input is not a
// struct, no field matches the reserved set, or a matched field is
// not itself a struct.
func RegisterHandlerWithInput[T, Input any](e *Engine[T], name string, h func(*Context[T], Input) error, opts ...HandlerOption) {
	inputType := reflect.TypeOf((*Input)(nil)).Elem()
	binders, autoOpts := planInputBinders(inputType)

	wrapped := func(c *Context[T]) error {
		inputVal := reflect.New(inputType).Elem()
		for _, b := range binders {
			target := inputVal.Field(b.fieldIndex).Addr().Interface()
			if err := b.bind(c.Ctx, e.validator, target); err != nil {
				return e.bindError(c, err)
			}
		}
		return h(c, inputVal.Interface().(Input))
	}

	all := append(autoOpts, opts...)
	e.RegisterHandler(name, wrapped, all...)
}

// inputBinder records, for one recognised field on the Input struct,
// where it lives (fieldIndex) and how to parse it. Built once at
// registration time and reused for every request.
type inputBinder struct {
	fieldIndex int
	bind       func(c *fiber.Ctx, v bind.Validator, target any) error
}

// planInputBinders walks Input's fields once and returns the binder
// list + matching HandlerOption defaults for OpenAPI. Panics with
// *Error on misuse.
func planInputBinders(inputType reflect.Type) ([]inputBinder, []HandlerOption) {
	if inputType.Kind() != reflect.Struct {
		panic(&Error{Stage: "register", Code: CodeRegisterMisuse,
			Message: fmt.Sprintf("RegisterHandlerWithInput requires a struct, got %s", inputType.Kind())})
	}

	var binders []inputBinder
	var opts []HandlerOption
	for i := 0; i < inputType.NumField(); i++ {
		f := inputType.Field(i)
		var (
			binder func(c *fiber.Ctx, v bind.Validator, target any) error
			opt    HandlerOption
		)
		switch f.Name {
		case "Body":
			binder = bindBodyField
			opt = WithBody(reflect.New(f.Type).Elem().Interface())
		case "Params":
			binder = bindParamsField
			opt = WithParams(reflect.New(f.Type).Elem().Interface())
		case "Query":
			binder = bindQueryField
			opt = WithQuery(reflect.New(f.Type).Elem().Interface())
		case "Headers":
			binder = bindHeaderField
			opt = WithHeaders(reflect.New(f.Type).Elem().Interface())
		default:
			continue
		}
		if f.Type.Kind() != reflect.Struct {
			panic(&Error{Stage: "register", Code: CodeRegisterMisuse,
				Message: fmt.Sprintf("RegisterHandlerWithInput field %q must be a struct, got %s",
					f.Name, f.Type.Kind())})
		}
		if f.PkgPath != "" { // unexported
			panic(&Error{Stage: "register", Code: CodeRegisterMisuse,
				Message: fmt.Sprintf("RegisterHandlerWithInput field %q must be exported", f.Name)})
		}
		binders = append(binders, inputBinder{fieldIndex: i, bind: binder})
		opts = append(opts, opt)
	}

	if len(binders) == 0 {
		panic(&Error{Stage: "register", Code: CodeRegisterMisuse,
			Message: "RegisterHandlerWithInput received an Input with no recognised fields (expected at least one of Body/Params/Query/Headers)"})
	}
	return binders, opts
}

func bindBodyField(c *fiber.Ctx, v bind.Validator, target any) error {
	if err := c.BodyParser(target); err != nil {
		return fmt.Errorf("%w: %w", bind.ErrParseBody, err)
	}
	if v != nil {
		if err := v.Struct(target); err != nil {
			return fmt.Errorf("%w: %w", bind.ErrValidateBody, err)
		}
	}
	return nil
}

func bindParamsField(c *fiber.Ctx, v bind.Validator, target any) error {
	if err := c.ParamsParser(target); err != nil {
		return fmt.Errorf("%w: %w", bind.ErrParseParams, err)
	}
	if v != nil {
		if err := v.Struct(target); err != nil {
			return fmt.Errorf("%w: %w", bind.ErrValidateParams, err)
		}
	}
	return nil
}

func bindQueryField(c *fiber.Ctx, v bind.Validator, target any) error {
	if err := c.QueryParser(target); err != nil {
		return fmt.Errorf("%w: %w", bind.ErrParseQuery, err)
	}
	if v != nil {
		if err := v.Struct(target); err != nil {
			return fmt.Errorf("%w: %w", bind.ErrValidateQuery, err)
		}
	}
	return nil
}

func bindHeaderField(c *fiber.Ctx, v bind.Validator, target any) error {
	if err := c.ReqHeaderParser(target); err != nil {
		return fmt.Errorf("%w: %w", bind.ErrParseHeader, err)
	}
	if v != nil {
		if err := v.Struct(target); err != nil {
			return fmt.Errorf("%w: %w", bind.ErrValidateHeader, err)
		}
	}
	return nil
}
