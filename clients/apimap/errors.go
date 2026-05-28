package apimap

import (
	"fmt"
	"strconv"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Build-time error codes returned in *errs.Error.Code from Engine.Build
// (collected via errors.Join when multiple validation failures co-occur).
const (
	CodeAlreadyBuilt              = "apimap_already_built"
	CodeNoClients                 = "apimap_no_clients"
	CodeDuplicateClient           = "apimap_duplicate_client"
	CodeDuplicateEndpoint         = "apimap_duplicate_endpoint"
	CodeMissingClientName         = "apimap_missing_client_name"
	CodeInvalidBaseURL            = "apimap_invalid_base_url"
	CodeInvalidMethod             = "apimap_invalid_method"
	CodeInvalidPathVar            = "apimap_invalid_path_var"
	CodeInvalidEncode             = "apimap_invalid_encode"
	CodeInvalidDecode             = "apimap_invalid_decode"
	CodeRegisteredEndpointMissing = "apimap_registered_endpoint_missing"
)

// Runtime error codes returned from Do / Decode / Exchange (the
// per-endpoint status codes are constructed dynamically — see
// codeForEndpointStatus).
const (
	CodeUnknownEndpoint       = "apimap_unknown_endpoint"
	CodeMissingPathVar        = "apimap_missing_path_var"
	CodeUnknownPathVar        = "apimap_unknown_path_var"
	CodeMissingRequestURL     = "apimap_missing_request_url"
	CodeURLConflict           = "apimap_url_conflict"
	CodeEncodeFailed          = "apimap_encode_failed"
	CodeDecodeFailed          = "apimap_decode_failed"
	CodeUnsupportedBodyType   = "apimap_unsupported_body_type"
	CodeUnsupportedDecodeType = "apimap_unsupported_decode_type"
)

// Auth + env-var substitution error codes (returned from LoadFile /
// LoadBytes / Build).
const (
	CodeAuthInvalidType     = "apimap_auth_invalid_type"
	CodeAuthMissingField    = "apimap_auth_missing_field"
	CodeUnknownCustomAuth   = "apimap_unknown_custom_auth"
	CodeDuplicateCustomAuth = "apimap_duplicate_custom_auth"
	CodeEnvVarUnset         = "apimap_env_var_unset"
	CodeEnvVarMalformed     = "apimap_env_var_malformed"
)

// statusToKind maps an HTTP status code to the errs.Kind we surface from
// Decode/Exchange. Codes outside the explicit set fall back to Validation
// (4xx) or Internal (5xx).
func statusToKind(status int) xerrs.Kind {
	switch status {
	case 400:
		return xerrs.KindValidation
	case 401:
		return xerrs.KindUnauthorized
	case 403:
		return xerrs.KindPermission
	case 404:
		return xerrs.KindNotFound
	case 409:
		return xerrs.KindConflict
	case 429:
		return xerrs.KindRateLimited
	}
	if status >= 500 {
		return xerrs.KindInternal
	}
	if status >= 400 {
		return xerrs.KindValidation
	}
	return xerrs.KindUnknown
}

// codeForEndpointStatus produces the stable per-endpoint, per-status
// error Code surfaced in *errs.Error.Code.
func codeForEndpointStatus(client, endpoint string, status int) string {
	return fmt.Sprintf("apimap_%s_%s_%s", client, endpoint, suffixForStatus(status))
}

// suffixForStatus produces the short human-readable suffix appended to
// per-endpoint error codes.
func suffixForStatus(status int) string {
	switch status {
	case 400:
		return "bad_request"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 409:
		return "conflict"
	case 429:
		return "rate_limited"
	}
	if status >= 500 {
		return "server_error"
	}
	if status >= 400 {
		return "client_error"
	}
	return "status_" + strconv.Itoa(status)
}
