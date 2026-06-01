package sessions

// Stable error Code constants returned by the package.
const (
	// CodeMissingSession — Middleware in [Required] mode saw no
	// cookie OR the cookie pointed to an expired / deleted
	// session.
	CodeMissingSession = "sessions_missing"

	// CodeInvalidSessionID — Cookie value failed shape validation
	// (length / charset). Often means cookie tampering.
	CodeInvalidSessionID = "sessions_invalid_id"

	// CodeStoreFailed — SessionStore.Get / Create / Touch / Delete
	// returned a backend error.
	CodeStoreFailed = "sessions_store_failed"

	// CodeClaimsDecode — Stored Claims couldn't decode into the
	// configured C — usually a schema change between deploys.
	// Middleware treats this as a forced logout.
	CodeClaimsDecode = "sessions_claims_decode"

	// CodeInvalidConfig — Config was missing required fields (TTL
	// == 0, nil Store, …).
	CodeInvalidConfig = "sessions_invalid_config"
)
