package ratelimit

// Stable error Code constants produced by this package.
const (
	// CodeInvalidConfig — NewRedis received an invalid Config
	// (empty KeyPrefix, Limit <= 0, or non-positive Window).
	CodeInvalidConfig = "ratelimit_invalid_config"

	// CodeBackendUnavailable — Allow could not reach Redis. The
	// caller decides fail-open (allow on error) vs fail-closed.
	// The kit factory chooses fail-open so a Redis blip does not
	// turn into a service-wide 429 storm.
	CodeBackendUnavailable = "ratelimit_backend_unavailable"
)
