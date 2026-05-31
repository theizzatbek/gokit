package fibermount

import (
	"strconv"
	"time"

	"github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
)

// CodeInvalidArg — idempotency_key factory received an unrecognised
// or malformed YAML arg.
const CodeInvalidArg = "fibermount_idempotency_key_invalid_arg"

// idempotencyKeyFactory returns a fibermap MiddlewareFactoryFunc that
// builds [fibermap.IdempotencyKey] from the YAML args. Bound to the
// caller-supplied store at registration time.
//
// Supported args (positional, all optional):
//
//	[0] ttl       — e.g. "1h", "30m" — passed to time.ParseDuration
//	                Optional second token: "required" enables
//	                WithIdempotencyRequired.
//
// Forward-compatible: unknown args return *errs.Error{Code:
// CodeInvalidArg} so a typo surfaces at engine.Mount instead of
// silently relaxing the contract.
func idempotencyKeyFactory[T any](store fibermap.IdempotencyStore) fibermap.MiddlewareFactoryFunc[T] {
	return func(args []string) (fibermap.MiddlewareFunc[T], error) {
		var opts []fibermap.IdempotencyOption
		for i, raw := range args {
			if i == 0 {
				d, err := parseDurationArg(raw)
				if err != nil {
					return nil, err
				}
				if d > 0 {
					opts = append(opts, fibermap.WithIdempotencyTTL(d))
				}
				continue
			}
			switch raw {
			case "required":
				opts = append(opts, fibermap.WithIdempotencyRequired())
			default:
				return nil, errs.Validationf(CodeInvalidArg,
					"idempotency_key: unknown arg %q at position %d", raw, i)
			}
		}
		h := fibermap.IdempotencyKey(store, opts...)
		return func(c *fibermap.Context[T]) error { return h(c.Ctx) }, nil
	}
}

// parseDurationArg accepts either a Go time.Duration string ("1h30m",
// "45s") or a bare integer interpreted as seconds. Empty input is
// treated as zero (== "use the middleware default TTL").
func parseDurationArg(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d, nil
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	return 0, errs.Validationf(CodeInvalidArg,
		"idempotency_key: ttl %q is neither time.Duration nor seconds-int", raw)
}
