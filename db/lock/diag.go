package lock

import (
	"context"
)

// IsHeld reports whether any backend currently holds the lock.
// Implementation: optimistically TryAcquires and immediately releases
// — if the acquire was contended, the lock is held; if it succeeded,
// no one was holding it at the instant of the check.
//
// Intended for /admin observability and operator runbook scripts,
// NOT for control flow — by the time the caller reads the boolean,
// another backend may have grabbed the lock. Treat the result as a
// snapshot.
//
// Returns (false, nil) when nobody holds the lock OR an error
// occurred during TryAcquire (the err is non-nil in the latter case,
// so callers can disambiguate via the err return).
func (l *Lock) IsHeld(ctx context.Context) (bool, error) {
	ok, release, err := l.TryAcquire(ctx)
	if err != nil {
		return false, err
	}
	if ok {
		release()
		return false, nil
	}
	return true, nil
}
