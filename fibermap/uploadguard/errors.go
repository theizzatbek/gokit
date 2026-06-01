package uploadguard

// Stable error Code constants produced by the middleware.
const (
	// CodeFieldMissing — the named form field was not present in
	// the inbound multipart body. WithRequiredField defaults true;
	// optional uploads (passthrough on miss) are opt-in via
	// WithOptionalField.
	CodeFieldMissing = "uploadguard_field_missing"

	// CodeSizeExceeded — file body exceeded WithMaxSize.
	CodeSizeExceeded = "uploadguard_size_exceeded"

	// CodeMIMENotAllowed — sniffed Content-Type was not in the
	// WithAllowedMIME allowlist.
	CodeMIMENotAllowed = "uploadguard_mime_not_allowed"

	// CodeUploadFailed — S3 Put returned an error during streaming.
	CodeUploadFailed = "uploadguard_upload_failed"

	// CodeOpenFailed — couldn't open the multipart.FileHeader. Rare
	// — Fiber's multipart layer would already have rejected a
	// malformed body.
	CodeOpenFailed = "uploadguard_open_failed"

	// CodeInvalidConfig — Guard was called with empty fieldName or
	// otherwise-broken options.
	CodeInvalidConfig = "uploadguard_invalid_config"
)
