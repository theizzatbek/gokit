package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// composeAfterConnect builds a single pgxpool.Config.AfterConnect hook
// from the kit-internal statement_timeout setter + any user-supplied
// ConnInitFn chain. Returns nil when both are absent so pgx falls back
// to its native (nil) default.
//
// Order:
//  1. statement_timeout (kit-internal, when WithDefaultStatementTimeout > 0)
//  2. each WithConnInit hook in registration order
//
// First non-nil return short-circuits and surfaces to pgx — pgx then
// discards the connection and re-dials.
func composeAfterConnect(o *options) func(context.Context, *pgx.Conn) error {
	if o.statementTimeout <= 0 && len(o.connInit) == 0 {
		return nil
	}
	timeoutMS := int(o.statementTimeout.Milliseconds())
	hooks := o.connInit
	return func(ctx context.Context, conn *pgx.Conn) error {
		if timeoutMS > 0 {
			if _, err := conn.Exec(ctx, fmt.Sprintf("SET statement_timeout = %d", timeoutMS)); err != nil {
				return fmt.Errorf("db: set statement_timeout: %w", err)
			}
		}
		for _, h := range hooks {
			if err := h(ctx, conn); err != nil {
				return err
			}
		}
		return nil
	}
}
