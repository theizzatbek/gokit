package errs

import "log/slog"

// LogValue makes *Error participate in slog as a structured group:
//
//	logger.Error("...", "err", e)
//
// produces attributes err.kind, err.code, err.message, plus err.cause when
// non-nil and err.details when non-empty.
func (e *Error) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("kind", e.Kind.String()),
		slog.String("code", e.Code),
		slog.String("message", e.Message),
	}
	if e.Cause != nil {
		attrs = append(attrs, slog.String("cause", e.Cause.Error()))
	}
	if len(e.Details) > 0 {
		attrs = append(attrs, slog.Any("details", e.Details))
	}
	return slog.GroupValue(attrs...)
}
