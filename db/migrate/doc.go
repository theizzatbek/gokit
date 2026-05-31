// Package migrate is the kit's zero-dependency Postgres migration
// runner. Conventions over configuration:
//
//   - Migrations live as files in an `embed.FS` you supply.
//   - File names follow `NNNN_name.sql` for Up migrations and
//     `NNNN_name.down.sql` for the matching Down (Down is optional).
//     The NNNN prefix is the version key and dictates ordering.
//   - Each file runs inside a transaction by default. To opt out
//     for statements Postgres cannot run transactionally (e.g.
//     `CREATE INDEX CONCURRENTLY`), prefix the file with the
//     directive `-- @migrate:no-transaction` on the first non-empty
//     line.
//   - The runner tracks applied versions in a `schema_migrations`
//     table (`version text PRIMARY KEY, applied_at timestamptz`).
//
// # Sketch
//
//	//go:embed migrations/*.sql
//	var migrationsFS embed.FS
//
//	if err := migrate.Up(ctx, svc.DB, migrationsFS); err != nil {
//	    return err
//	}
//
// service.WithMigrations(migrationsFS) wires this automatically
// in the service bundle — see service/README.md.
//
// # Non-goals
//
//   - Database-agnostic dialects. The runner targets pgx + Postgres
//     by design; multi-database support would compromise the SQL-
//     forward feel.
//   - Cross-version rollback graphs. Down operates strictly on the
//     most recently applied N migrations in reverse order.
//   - Online schema changes. Pgroll / pg-osc are out of scope; the
//     runner just executes whatever DDL the .sql file contains.
package migrate
