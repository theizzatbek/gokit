package auditfm

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/audit"
	"github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
)

// Spec configures one [Wrap] / [Emit] call. Action is the only
// required field; everything else has defaults that produce a
// reasonable event without further wiring.
type Spec struct {
	// Action is the audit Action stamped on the emitted Event
	// (e.g. "license.revoke", "user.delete"). Required.
	//
	// Empty Action causes [Wrap] to skip emission entirely and log
	// a warning on the optional [Spec.Logger]; the handler still
	// runs. The kit prefers silent skip over panic / handler-fail
	// because a misconfigured audit spec must not break the
	// privileged endpoint it was supposed to record.
	Action string

	// SubjectFn pulls the actor's subject identifier from the
	// request context. Typical implementation reaches for the
	// auth-populated Locals slot:
	//
	//	SubjectFn: func(c *fiber.Ctx) string {
	//	    p, _ := auth.From[Claims](c)
	//	    return p.Subject
	//	}
	//
	// nil leaves Actor.Subject empty. The kit doesn't reach into
	// the auth/internal/principalkey slot directly because the key
	// is intentionally unexported.
	SubjectFn func(c *fiber.Ctx) string

	// TargetFn returns the [audit.Target] (resource type + ID +
	// optional Name) the action operated on. nil leaves Target
	// empty — appropriate for actions without a resource target
	// (e.g. "auth.login").
	TargetFn func(c *fiber.Ctx) audit.Target

	// MetadataFn returns the per-event Metadata map (folded into
	// Event.Metadata). nil leaves Metadata nil. Use to capture
	// per-request context like "reason: 'compliance hold'" or
	// "ip_geo: 'us-east-1'".
	MetadataFn func(c *fiber.Ctx) map[string]any

	// OutcomeFn maps the handler's returned error to an
	// [audit.Outcome]. nil falls back to [DefaultOutcome] which
	// maps:
	//
	//	nil                                              → Success
	//	*errs.Error{Kind: Unauthorized | Permission}     → Denied
	//	anything else                                    → Failure
	//
	// Override when a typed validation error should land as Denied
	// (some compliance frameworks track validation rejections as
	// authorization failures).
	OutcomeFn func(handlerErr error) audit.Outcome

	// Logger receives Warn entries when audit emission itself
	// fails (Action empty, store unavailable, etc). The handler
	// path is never affected — audit issues are surfaced to ops
	// observability separately. nil = silent.
	Logger *slog.Logger
}

// Wrap decorates fn with audit emission. The decorated handler:
//
//  1. Invokes fn(c) and captures its returned error.
//  2. Builds an [audit.Event] from the spec + the captured error.
//  3. Calls logger.Log on a derived context so the audit append
//     isn't cancelled by the request's ctx (which may already be
//     done by the time the response was written). The derived
//     context inherits values for tracing but ignores
//     cancellation; emission timing-out independently is the
//     store's concern.
//  4. Returns the captured error unchanged so the handler's normal
//     error-mapping (errs.HTTP via fibermap.ErrorHandler) still
//     runs.
//
// Audit emission failures are logged on spec.Logger (Warn level)
// and never bubble back to the caller — the handler's own outcome
// is what the HTTP response should reflect.
//
// Generic constraint: T is the per-request payload type the engine
// is parameterised by (the same T from [fibermap.Engine[T]]).
func Wrap[T any](logger *audit.Logger, spec Spec, fn fibermap.HandlerFunc[T]) fibermap.HandlerFunc[T] {
	if logger == nil {
		// Refusing nil-logger at registration time is louder than
		// allowing silent skip — the audit gap an integrator added
		// by forgetting to wire the logger should surface
		// immediately, not at the first privileged request.
		panic(&fibermap.Error{
			Stage:   "register",
			Code:    fibermap.CodeRegisterMisuse,
			Message: "auditfm.Wrap: nil *audit.Logger",
		})
	}
	return func(c *fibermap.Context[T]) error {
		handlerErr := fn(c)
		Emit(c.Ctx, logger, spec, handlerErr)
		return handlerErr
	}
}

// Emit is the lower-level building block for callers who prefer to
// inline the audit emission inside a typed-bind handler — the
// pattern looks like:
//
//	fibermap.RegisterHandlerWithParams(eng, "license.revoke",
//	    func(c *fibermap.Context[AppCtx], p RevokeParams) error {
//	        err := h.RevokeLicense(c, p)
//	        auditfm.Emit(c.Ctx, logger, auditfm.Spec{
//	            Action:   "license.revoke",
//	            TargetFn: func(c *fiber.Ctx) audit.Target {
//	                return audit.Target{Type: "license", ID: p.ID}
//	            },
//	        }, err)
//	        return err
//	    })
//
// Emit is identical to the post-execution step [Wrap] runs — call
// it once per request, after the handler decided its outcome.
// Calling it twice on the same request will emit two events
// (intentional; the caller might want a "started" + "finished" pair
// for long-running ops).
//
// Returning vs panicking: Emit never returns an error and never
// panics on emit-failure. Audit-store failures are surfaced via
// spec.Logger.Warn so ops sees them; the handler path stays
// transparent.
func Emit(c *fiber.Ctx, logger *audit.Logger, spec Spec, handlerErr error) {
	if logger == nil || spec.Action == "" {
		if spec.Logger != nil {
			spec.Logger.Warn("auditfm: skip emit",
				"reason", emitSkipReason(logger, spec),
				"action", spec.Action)
		}
		return
	}

	outcome := DefaultOutcome(handlerErr)
	if spec.OutcomeFn != nil {
		outcome = spec.OutcomeFn(handlerErr)
	}

	event := audit.Event{
		Action:  spec.Action,
		Outcome: outcome,
		Actor:   buildActor(c, spec.SubjectFn),
	}
	if spec.TargetFn != nil {
		event.Target = spec.TargetFn(c)
	}
	if spec.MetadataFn != nil {
		event.Metadata = spec.MetadataFn(c)
	}

	// Use a Background-derived context so emission outlives the
	// request's ctx (which may already be Done by the time the
	// response writes complete). Tracing values are dropped along
	// with cancellation — auditing is the durable layer and
	// shouldn't be torn down by request lifecycle.
	emitCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := logger.Log(emitCtx, event); err != nil && spec.Logger != nil {
		spec.Logger.Warn("auditfm: audit append failed",
			"action", spec.Action,
			"outcome", string(outcome),
			"err", err.Error())
	}
}

// DefaultOutcome is the classifier [Wrap] / [Emit] use when
// [Spec.OutcomeFn] is nil:
//
//	nil                                          → audit.Success
//	*errs.Error{Kind: Unauthorized | Permission} → audit.Denied
//	anything else                                → audit.Failure
//
// Use directly to layer custom logic on top of the default:
//
//	OutcomeFn: func(err error) audit.Outcome {
//	    if errors.Is(err, db.ErrConflict) { return audit.Failure }
//	    return auditfm.DefaultOutcome(err)
//	}
func DefaultOutcome(handlerErr error) audit.Outcome {
	if handlerErr == nil {
		return audit.Success
	}
	var e *errs.Error
	if errors.As(handlerErr, &e) {
		switch e.Kind {
		case errs.KindUnauthorized, errs.KindPermission:
			return audit.Denied
		}
	}
	return audit.Failure
}

// buildActor populates the Actor from the request context. IP and
// UA come from fiber.Ctx directly; Subject comes from the optional
// SubjectFn. Type is left empty by default — callers wanting "user"
// vs "service" vs "system" classification can set it in their
// SubjectFn via a closure capturing the right value, or in
// MetadataFn if it's per-request.
func buildActor(c *fiber.Ctx, subjectFn func(*fiber.Ctx) string) audit.Actor {
	a := audit.Actor{
		IP: c.IP(),
		UA: c.Get(fiber.HeaderUserAgent),
	}
	if subjectFn != nil {
		a.Subject = subjectFn(c)
	}
	return a
}

// emitSkipReason returns the human-readable reason Emit decided to
// skip — used in the spec.Logger.Warn breadcrumb so a misconfigured
// spec surfaces in ops logs.
func emitSkipReason(logger *audit.Logger, spec Spec) string {
	switch {
	case logger == nil:
		return "nil logger"
	case spec.Action == "":
		return "empty action"
	default:
		return "unknown"
	}
}
