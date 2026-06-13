package fibermap

import (
	"errors"

	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/errs/errsval"
	"github.com/theizzatbek/gokit/fibermap/bind"
)

// ErrsvalBindError is the recommended [Engine.SetBindErrorHandler]
// value for services that use the kit's *errs.Error contract for HTTP
// failure modes. It maps every parse / validate failure produced by
// [bind].Body / Query / Params / Header (and by [RegisterHandlerWithInput])
// into the kit's standard `{code, message, details[]}` JSON wire
// shape:
//
//	eng.SetBindErrorHandler(fibermap.ErrsvalBindError[AppCtx])
//
// The helper is a no-op when err is nil. Otherwise it:
//
//  1. Picks a source-aware Code by walking the [bind] sentinel chain
//     (errors.Is against bind.ErrParseBody / ErrValidateBody / …):
//     "invalid_body" / "invalid_query" / "invalid_params" /
//     "invalid_header". Anything that does not match the four
//     sources falls through to "invalid_request".
//
//  2. Calls [errsval.FromValidator] to recover per-field
//     [errs.FieldError] details when the wrapped error is a
//     `validator.ValidationErrors` chain (which is what the kit's
//     bind helpers preserve since v1.0.1 via the errors.Join wrap).
//     If extraction succeeds the resulting *errs.Error is cloned
//     with the source-specific Code; otherwise the helper falls back
//     to a single-field validation error built from `err.Error()`.
//
//  3. Renders the *errs.Error to status + JSON via [errs.HTTP] and
//     writes both via *fibermap.Context (which embeds *fiber.Ctx).
//
// # Default-handler vs ErrsvalBindError trade-off
//
// The kit ships a plain default (`{"error": "<message>"}` 400) so the
// `fibermap` package stays consumable without the caller buying into
// the [errs] / [errsval] convention. Production services that use
// the kit's typed errors elsewhere should call SetBindErrorHandler
// with this helper at engine-construction time — once — and forget
// it.
//
// # Behaviour for non-bind errors
//
// Anything that doesn't match a bind sentinel (e.g. a custom handler
// returning a non-bind error through the same channel) is mapped to
// "invalid_request" with the raw `err.Error()` text. Callers can
// still chain a custom handler in front of [ErrsvalBindError] if they
// need different routing for a specific source error.
func ErrsvalBindError[T any](c *Context[T], err error) error {
	if err == nil {
		return nil
	}
	code := bindSourceCode(err)

	out, ok := buildErrsvalError(err, code)
	if !ok {
		out = xerrs.Validation(code, err.Error())
	}

	status, body := xerrs.HTTP(out)
	return c.Status(status).JSON(body)
}

// buildErrsvalError attempts to recover per-field validation details
// via errsval.FromValidator. Returns the converted *errs.Error with
// the source-specific code on success; (nil, false) when the wrapped
// chain is not a validator.ValidationErrors (parse-stage errors,
// non-validator handler errors).
func buildErrsvalError(err error, code string) (*xerrs.Error, bool) {
	conv := errsval.FromValidator(err)
	var e *xerrs.Error
	if !errors.As(conv, &e) {
		return nil, false
	}
	// Clone with the source-specific Code so downstream alerting can
	// branch on "invalid_body" vs "invalid_query" instead of a single
	// generic "validation_failed" code.
	return xerrs.Validation(code, e.Message, e.Details...), true
}

// bindSourceCode walks the bind sentinel chain to pick a
// source-aware Code. Errors that match none of the four sources
// fall through to "invalid_request".
func bindSourceCode(err error) string {
	switch {
	case errors.Is(err, bind.ErrValidateBody), errors.Is(err, bind.ErrParseBody):
		return "invalid_body"
	case errors.Is(err, bind.ErrValidateQuery), errors.Is(err, bind.ErrParseQuery):
		return "invalid_query"
	case errors.Is(err, bind.ErrValidateParams), errors.Is(err, bind.ErrParseParams):
		return "invalid_params"
	case errors.Is(err, bind.ErrValidateHeader), errors.Is(err, bind.ErrParseHeader):
		return "invalid_header"
	default:
		return "invalid_request"
	}
}
