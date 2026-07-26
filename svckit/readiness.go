package svckit

import (
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/fibermap"
)

// readinessCheckers assembles the live checker set: DB first (the
// only core subsystem that has one), followed by s.opts.readinessCheckers
// — the single accumulator both WithReadinessChecker and mods'
// Host.AddReadinessChecker (Build phase) write to. See
// WithReadinessChecker's godoc for the resulting order (user checkers
// end up before mod-contributed ones).
//
// There is no subsystem enumeration here, and there must not be one:
// a mod that knows how to check itself appends its own checker.
func (s *Service[T, C]) readinessCheckers() []fibermap.Checker {
	checkers := make([]fibermap.Checker, 0, 1+len(s.opts.readinessCheckers))
	if s.DB != nil {
		checkers = append(checkers, db.NewChecker(s.DB, ""))
	}
	checkers = append(checkers, s.opts.readinessCheckers...)
	return checkers
}
