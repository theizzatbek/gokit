package runbook

// Stable error Code constants produced by this package.
const (
	// CodeNilStore — New received a nil Store. Refused immediately
	// rather than swallowing flag writes into the void.
	CodeNilStore = "runbook_nil_store"

	// CodeStoreFailed — backend Store returned an error.
	CodeStoreFailed = "runbook_store_failed"

	// CodeInvalidFlagName — flag name failed shape validation
	// (empty, too long, or contains unsupported characters).
	CodeInvalidFlagName = "runbook_invalid_flag_name"
)
