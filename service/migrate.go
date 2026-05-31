package service

import (
	"context"

	"github.com/theizzatbek/gokit/db/migrate"
)

// runMigrations applies WithMigrations's embed.FS via migrate.Up
// when both DB and the FS are configured. No-op otherwise (DB
// missing → schema is irrelevant; FS missing → caller opts out).
//
// Failures surface unchanged from db/migrate — *errs.Error with
// CodeBootstrapFailed / CodeApplyFailed / CodeInvalidFilename and
// friends so dashboards can switch on the original Code.
func (s *Service[T, C]) runMigrations(ctx context.Context) error {
	if s.opts.migrationsFS == nil || s.DB == nil {
		return nil
	}
	if s.logger != nil {
		s.logger.Info("service: applying migrations")
	}
	return migrate.Up(ctx, s.DB, s.opts.migrationsFS)
}
