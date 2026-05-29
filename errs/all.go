package errs

import "errors"

// All walks err's tree and returns every *Error it can reach, flattened.
//
// The walk handles both shapes Go uses for wrapping:
//
//   - The errors.Join([]error) shape — returns an unnamed type that
//     implements Unwrap() []error. Each child is walked recursively.
//   - The single-cause shape — *Error.Unwrap() (and any other type
//     implementing Unwrap() error). The cause is walked recursively.
//
// Returns nil when err is nil. Non-*Error nodes are skipped (only
// their children, if any, are visited). Duplicates are not deduped —
// the order matches the depth-first walk order.
//
// Typical use: enumerating per-field validation failures returned as
// an aggregated errors.Join from a build / parse step:
//
//	if err := cfg.validate(); err != nil {
//	    for _, e := range errs.All(err) {
//	        log.Warn("config issue", "code", e.Code, "msg", e.Message)
//	    }
//	    return err
//	}
func All(err error) []*Error {
	if err == nil {
		return nil
	}
	var out []*Error
	walkAll(err, &out)
	return out
}

func walkAll(err error, out *[]*Error) {
	if err == nil {
		return
	}
	if e, ok := err.(*Error); ok {
		*out = append(*out, e)
		// fall through so Cause (via Unwrap) also gets walked — a
		// wrapped *Error chain (Wrap → Cause *Error → …) surfaces every
		// layer.
	}
	// errors.Join returns an unnamed type with Unwrap() []error. Any
	// custom multi-error type can opt into the same shape.
	if u, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range u.Unwrap() {
			walkAll(child, out)
		}
		return
	}
	if u := errors.Unwrap(err); u != nil {
		walkAll(u, out)
	}
}
