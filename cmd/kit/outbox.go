package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"
)

const usageOutbox = `kit outbox — inspect the transactional outbox

Usage:
  kit outbox status [--dsn DSN]

Prints pending count, oldest pending age, top recent failures.
Useful for incident response when the readiness probe surfaces
backlog or operators want to know whether a worker is keeping up.
`

func runOutbox(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageOutbox)
		return errors.New("outbox: subcommand required")
	}
	switch args[0] {
	case "status":
		return outboxStatus(ctx, args[1:])
	case "help", "--help", "-h":
		fmt.Fprint(os.Stdout, usageOutbox)
		return nil
	default:
		fmt.Fprint(os.Stderr, usageOutbox)
		return fmt.Errorf("outbox: unknown subcommand %q", args[0])
	}
}

func outboxStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("outbox status", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "postgres URL (falls back to DATABASE_URL env)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, err := loadDB(ctx, *dsn)
	if err != nil {
		return err
	}
	defer d.Close()

	var pending int
	var oldest *time.Time
	var attempted int
	var maxAttempts int
	row := d.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE published_at IS NULL),
			MIN(created_at) FILTER (WHERE published_at IS NULL),
			count(*) FILTER (WHERE published_at IS NULL AND attempts > 0),
			COALESCE(MAX(attempts) FILTER (WHERE published_at IS NULL), 0)
		FROM outbox
	`)
	if err := row.Scan(&pending, &oldest, &attempted, &maxAttempts); err != nil {
		return err
	}
	fmt.Printf("pending:        %d\n", pending)
	if oldest != nil {
		fmt.Printf("oldest_pending: %s (%s ago)\n",
			oldest.Format(time.RFC3339), time.Since(*oldest).Round(time.Second))
	} else {
		fmt.Println("oldest_pending: (none)")
	}
	fmt.Printf("with_retries:   %d\n", attempted)
	fmt.Printf("max_attempts:   %d\n", maxAttempts)

	if pending == 0 {
		return nil
	}
	// Top-5 recent failures so operators see the dominant failure mode.
	rows, err := d.Query(ctx, `
		SELECT event_type, attempts, last_error
		FROM outbox
		WHERE published_at IS NULL AND last_error IS NOT NULL
		ORDER BY attempts DESC, created_at DESC
		LIMIT 5
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	fmt.Println()
	fmt.Println("recent failures:")
	any := false
	for rows.Next() {
		var (
			eventType string
			attempts  int
			lastErr   string
		)
		if err := rows.Scan(&eventType, &attempts, &lastErr); err != nil {
			return err
		}
		fmt.Printf("  attempts=%d type=%s err=%s\n", attempts, eventType, lastErr)
		any = true
	}
	if !any {
		fmt.Println("  (no rows have non-null last_error)")
	}
	return nil
}
