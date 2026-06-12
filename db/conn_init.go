package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// composeAfterConnect builds a single pgxpool.Config.AfterConnect hook
// from the kit-internal UUID codec registration + the kit-internal
// statement_timeout setter + any user-supplied ConnInitFn chain.
// Always returns a non-nil hook: the UUID codec registration is
// unconditional (turns [16]byte → uuid encoding from "encoder not
// found" into a working default — see uuid_codec.go for context).
//
// Order on every fresh connection:
//  1. registerUUIDByteArrayCodec (kit-internal, unconditional)
//  2. statement_timeout (kit-internal, when WithDefaultStatementTimeout > 0)
//  3. each WithConnInit hook in registration order
//
// First non-nil return short-circuits and surfaces to pgx — pgx then
// discards the connection and re-dials.
func composeAfterConnect(o *options) func(context.Context, *pgx.Conn) error {
	timeoutMS := int(o.statementTimeout.Milliseconds())
	hooks := o.connInit
	return func(ctx context.Context, conn *pgx.Conn) error {
		if err := registerUUIDByteArrayCodec(ctx, conn); err != nil {
			return err
		}
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
