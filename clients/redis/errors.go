package redisclient

// Stable error Codes returned by Connect / Client methods. Use these
// constants when handling *errs.Error: callers may switch on Code
// without depending on the underlying go-redis error.
const (
	// CodeMissingURL — Config.URL was empty at Connect.
	CodeMissingURL = "redis_missing_url"

	// CodeInvalidURL — redis.ParseURL rejected Config.URL.
	CodeInvalidURL = "redis_invalid_url"

	// CodeConnectFailed — the initial PING failed after exhausting
	// ConnectMaxRetries. Wraps the last underlying go-redis error.
	CodeConnectFailed = "redis_connect_failed"
)
