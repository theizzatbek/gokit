package db

// Option configures Connect. Each option mutates a private options value;
// individual constructors (WithLogger, WithMetrics, ...) are added in Task 8.
type Option func(*options)

type options struct {
	// fields appear in Task 8.
}
