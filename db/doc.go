// Package db is the kit's pgx-based connection layer. It wraps *pgxpool.Pool
// with a functional transaction API, SQLSTATE-aware error mapping into
// *errs.Error, and opt-in slog/Prometheus observability.
//
// Lifecycle:
//
//	cfg := db.Config{Host: "localhost", Port: 5432, User: "app", Database: "app"}
//	d, err := db.Connect(ctx, cfg, db.WithLogger(logger))
//	if err != nil { return err }
//	defer d.Close()
//
//	err = d.Tx(ctx, func(tx *db.Tx) error {
//	    _, err := tx.Exec(ctx, "UPDATE accounts SET balance = balance - $1 WHERE id = $2", 100, fromID)
//	    return err
//	})
//
// Errors returned by every method are *errs.Error (see github.com/theizzatbek/fibermap/errs)
// so handlers can return them directly into fibermap.ErrorHandler.
//
// The package does NOT import fiber — it is usable from CLIs, workers, scripts.
// Subpackage db/sqb provides an opt-in squirrel query-builder wrapper.
package db
