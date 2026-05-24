package auth

import "fmt"

// Error codes returned in *errs.Error.Code. These are part of the public API:
// downstream services may switch on them and external clients may see them
// echoed in the WWW-Authenticate header on 401 responses.
const (
	CodeMissingToken         = "missing_token"
	CodeInvalidTokenScheme   = "invalid_token_scheme"
	CodeInvalidToken         = "invalid_token"
	CodeExpiredToken         = "expired_token"
	CodeMissingRefresh       = "missing_refresh"
	CodeRefreshInvalid       = "refresh_invalid"
	CodeRefreshExpired       = "refresh_expired"
	CodeRefreshReused        = "refresh_reused"
	CodeInvalidCredentials   = "invalid_credentials"
	CodeMissingScope         = "missing_scope"
	CodeMissingRole          = "missing_role"
	CodeInvalidFactoryArgs   = "invalid_factory_args"
	CodePasswordHashCorrupt  = "password_hash_corrupt"
	CodeKeyNotLoaded         = "key_not_loaded"
	CodeStoreUnavailable     = "store_unavailable"
)

// wwwAuthenticate renders an RFC 6750 §3 challenge string suitable for the
// WWW-Authenticate header on 401 responses from Bearer middleware.
func wwwAuthenticate(realm, errorCode string) string {
	return fmt.Sprintf(`Bearer realm=%q, error=%q`, realm, errorCode)
}
