package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/theizzatbek/gokit/db/migrate"
)

const usageMigrate = `kit migrate — apply / inspect Postgres schema migrations

Usage:
  kit migrate up     [--dir migrations/] [--dsn DSN]
  kit migrate down   [--steps N]         [--dir migrations/] [--dsn DSN]
  kit migrate status                     [--dir migrations/] [--dsn DSN]
  kit migrate version                    [--dsn DSN]

Migrations live in --dir as NNNN_name.sql + optional NNNN_name.down.sql.
DSN format: postgres://user:pw@host:port/db?sslmode=disable
            (falls back to DATABASE_URL env when --dsn is empty).
`

func runMigrate(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageMigrate)
		return errors.New("migrate: subcommand required")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "up":
		return migrateUp(ctx, rest)
	case "down":
		return migrateDown(ctx, rest)
	case "status":
		return migrateStatus(ctx, rest)
	case "version":
		return migrateVersion(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprint(os.Stdout, usageMigrate)
		return nil
	default:
		fmt.Fprint(os.Stderr, usageMigrate)
		return fmt.Errorf("migrate: unknown subcommand %q", sub)
	}
}

func migrateUp(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate up", flag.ContinueOnError)
	dir := fs.String("dir", "migrations", "directory containing NNNN_name.sql files")
	dsn := fs.String("dsn", "", "postgres URL (falls back to DATABASE_URL env)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, err := loadDB(ctx, *dsn)
	if err != nil {
		return err
	}
	defer d.Close()
	fsys := os.DirFS(*dir)
	if err := migrate.Up(ctx, d, fsys); err != nil {
		return err
	}
	v, _ := migrate.Version(ctx, d)
	fmt.Printf("migrate up: ok (version=%s)\n", v)
	return nil
}

func migrateDown(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate down", flag.ContinueOnError)
	dir := fs.String("dir", "migrations", "directory containing NNNN_name.sql files")
	dsn := fs.String("dsn", "", "postgres URL (falls back to DATABASE_URL env)")
	steps := fs.Int("steps", 1, "number of migrations to roll back")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, err := loadDB(ctx, *dsn)
	if err != nil {
		return err
	}
	defer d.Close()
	fsys := os.DirFS(*dir)
	if err := migrate.Down(ctx, d, fsys, *steps); err != nil {
		return err
	}
	v, _ := migrate.Version(ctx, d)
	fmt.Printf("migrate down: ok (steps=%d, version=%s)\n", *steps, v)
	return nil
}

func migrateStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate status", flag.ContinueOnError)
	dir := fs.String("dir", "migrations", "directory containing NNNN_name.sql files")
	dsn := fs.String("dsn", "", "postgres URL (falls back to DATABASE_URL env)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, err := loadDB(ctx, *dsn)
	if err != nil {
		return err
	}
	defer d.Close()
	fsys := os.DirFS(*dir)
	st, err := migrate.List(ctx, d, fsys)
	if err != nil {
		return err
	}
	fmt.Println(formatStatus(st))
	return nil
}

func migrateVersion(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate version", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "postgres URL (falls back to DATABASE_URL env)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, err := loadDB(ctx, *dsn)
	if err != nil {
		return err
	}
	defer d.Close()
	v, err := migrate.Version(ctx, d)
	if err != nil {
		return err
	}
	if v == "" {
		fmt.Println("(no migrations applied)")
		return nil
	}
	fmt.Println(v)
	return nil
}

// formatStatus produces an aligned 3-column listing of Version,
// Applied flag, and Name. Plain text — no ANSI colors so the output
// pipes cleanly into grep / awk.
func formatStatus(st []migrate.Status) string {
	if len(st) == 0 {
		return "(no migrations found)"
	}
	var b strings.Builder
	b.WriteString("VERSION  STATUS    NAME\n")
	for _, s := range st {
		mark := "pending"
		if s.Applied {
			mark = "applied"
		}
		fmt.Fprintf(&b, "%-8s %-9s %s\n", s.Version, mark, s.Name)
	}
	return b.String()
}
