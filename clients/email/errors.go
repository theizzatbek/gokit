package email

// Stable error Code constants produced by the package.
const (
	// CodeInvalidConfig — New received an empty/unknown Backend, or
	// the backend's required fields were missing.
	CodeInvalidConfig = "email_invalid_config"

	// CodeInvalidMessage — Send rejected a Message because of
	// missing From / no recipients / empty Subject AND body.
	CodeInvalidMessage = "email_invalid_message"

	// CodeSendFailed — backend reported an error. The original
	// SDK / SMTP / HTTP error is wrapped in Cause.
	CodeSendFailed = "email_send_failed"

	// CodeTemplateNotFound — Templates.Render was called with an
	// unknown template name.
	CodeTemplateNotFound = "email_template_not_found"

	// CodeTemplateExecFailed — html/text template Execute returned
	// an error. Usually a missing field on the supplied data.
	CodeTemplateExecFailed = "email_template_exec_failed"
)
