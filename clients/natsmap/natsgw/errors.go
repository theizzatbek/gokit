package natsgw

// Stable error Code constants returned by [Handler].
const (
	// CodeInvalidSubject — the subject extractor returned empty
	// (missing path param, malformed body field, etc.).
	CodeInvalidSubject = "natsgw_invalid_subject"

	// CodeSubjectNotAllowed — the subject was non-empty but not in
	// the [WithSubjectAllowlist]. Allowlists default to "any
	// registered publisher" so this fires only when the operator
	// explicitly opted in to gatekeeping.
	CodeSubjectNotAllowed = "natsgw_subject_not_allowed"

	// CodePayloadTooLarge — inbound body exceeded the configured
	// [WithMaxBodySize].
	CodePayloadTooLarge = "natsgw_payload_too_large"

	// CodePublishFailed — natsmap.PublishRaw returned an error
	// (unknown publisher, NATS transport blip, etc.). The
	// underlying error wraps in Cause.
	CodePublishFailed = "natsgw_publish_failed"

	// CodeValidationFailed — a [Validator] rejected the inbound
	// payload. The wrapped error carries the validator's reason so
	// the caller sees WHY (missing field, wrong shape, etc.).
	CodeValidationFailed = "natsgw_validation_failed"
)
