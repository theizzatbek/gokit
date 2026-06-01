package natsgw

import (
	"context"
	"encoding/json"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Validator inspects an inbound publish before it reaches NATS.
// Return nil to accept the payload, any non-nil error to reject it
// with HTTP 400 + Code [CodeValidationFailed] (the validator's
// error is wrapped as Cause so the rejection reason flows through
// to the API caller).
//
// Validators see the RAW body — the bytes the gateway is about to
// forward to natsmap.PublishRaw. They MUST NOT mutate body; the
// kit forwards exactly what came in. Transformation belongs in a
// Fiber middleware upstream where it has access to the full
// request context.
type Validator func(ctx context.Context, subject string, body []byte) error

// WithValidator installs a Validator that runs on EVERY inbound
// publish, after the subject allowlist + body-size check, before
// the NATS publish. Stack multiple WithValidator calls — they run
// in registration order; the first non-nil error wins.
//
//	natsgw.Handler(rt,
//	    natsgw.WithValidator(natsgw.ValidJSON()),
//	)
func WithValidator(fn Validator) Option {
	return func(c *config) {
		c.validators = append(c.validators, scopedValidator{fn: fn})
	}
}

// WithSubjectValidator runs fn ONLY when the inbound subject equals
// the supplied one. Stack multiple to cover different subjects with
// different validators.
//
//	natsgw.Handler(rt,
//	    natsgw.WithSubjectValidator("urlshort.link.visited",
//	        natsgw.UnmarshalAs[LinkVisited]()),
//	    natsgw.WithSubjectValidator("urlshort.link.created",
//	        natsgw.UnmarshalAs[LinkCreated]()),
//	)
func WithSubjectValidator(subject string, fn Validator) Option {
	return func(c *config) {
		c.validators = append(c.validators, scopedValidator{
			subject: subject, fn: fn,
		})
	}
}

// ValidJSON returns a Validator that only checks the body is a
// well-formed JSON value (object, array, string, number, true/false,
// null). Cheap — uses json.Valid which is allocation-free.
//
// Use as a coarse pre-check ("don't admit malformed JSON onto the
// bus") in combination with typed per-subject validators.
func ValidJSON() Validator {
	return func(_ context.Context, _ string, body []byte) error {
		if !json.Valid(body) {
			return xerrs.Validation(CodeValidationFailed,
				"body is not valid JSON")
		}
		return nil
	}
}

// UnmarshalAs returns a Validator that JSON-decodes the body into a
// zero-value T. Successful decode == OK; decode failure → rejection.
//
// Typical use is the per-subject typed contract: validate that the
// inbound payload is shaped exactly like the consumer expects on
// the other end of NATS. T must be JSON-decodable; the validator
// never touches the original body bytes.
//
//	natsgw.WithSubjectValidator("urlshort.link.visited",
//	    natsgw.UnmarshalAs[events.LinkVisited]())
func UnmarshalAs[T any]() Validator {
	return func(_ context.Context, _ string, body []byte) error {
		var v T
		if err := json.Unmarshal(body, &v); err != nil {
			return xerrs.Wrap(err, xerrs.KindValidation, CodeValidationFailed,
				"body does not decode into expected type")
		}
		return nil
	}
}
