package audit

import (
	"errors"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// OutcomeFromError classifies a handler error into an audit Outcome:
//
//	nil                                          → Success
//	*errs.Error{Kind: Unauthorized | Permission} → Denied
//	anything else                                → Failure
//
// This is the kit-wide classifier behind auditfm.DefaultOutcome and
// the auditmw middleware. In the kit idiom handlers RETURN errors and
// the app-level error handler maps them to HTTP statuses only after
// the middleware chain unwinds — so audit emitters must classify by
// the error, never by the not-yet-final response status.
func OutcomeFromError(err error) Outcome {
	if err == nil {
		return Success
	}
	var e *xerrs.Error
	if errors.As(err, &e) {
		switch e.Kind {
		case xerrs.KindUnauthorized, xerrs.KindPermission:
			return Denied
		}
	}
	return Failure
}
