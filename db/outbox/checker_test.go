package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/outbox"
	"github.com/theizzatbek/gokit/errs"
)

func TestChecker_NilSafe(t *testing.T) {
	var c *outbox.Checker
	if got := c.Name(); got != "outbox" {
		t.Errorf("nil Name = %q, want outbox", got)
	}
	if err := c.Check(context.Background()); err != nil {
		t.Errorf("nil Check = %v, want nil", err)
	}
}

func TestChecker_EmptyQueue_OK(t *testing.T) {
	d := freshDB(t)
	c := outbox.NewChecker(d)
	if err := c.Check(context.Background()); err != nil {
		t.Errorf("Check on empty outbox = %v, want nil", err)
	}
}

func TestChecker_BelowDepth_OK(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := d.Tx(ctx, func(tx *db.Tx) error {
			return outbox.Enqueue(ctx, tx, outbox.Event{
				EventType: "test.depth", Payload: []byte(`{}`),
			})
		}); err != nil {
			t.Fatal(err)
		}
	}
	c := outbox.NewChecker(d, outbox.WithMaxDepth(10))
	if err := c.Check(ctx); err != nil {
		t.Errorf("Check with depth 3 vs cap 10 = %v, want nil", err)
	}
}

func TestChecker_OverDepth_Fails(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := d.Tx(ctx, func(tx *db.Tx) error {
			return outbox.Enqueue(ctx, tx, outbox.Event{
				EventType: "test.depth", Payload: []byte(`{}`),
			})
		}); err != nil {
			t.Fatal(err)
		}
	}
	c := outbox.NewChecker(d, outbox.WithMaxDepth(3))
	err := c.Check(ctx)
	if err == nil {
		t.Fatal("Check should fail when depth > MaxDepth")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != outbox.CodeBacklog {
		t.Errorf("err code = %v, want outbox_backlog", e)
	}
}

func TestChecker_OverLag_Fails(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	if err := d.Tx(ctx, func(tx *db.Tx) error {
		return outbox.Enqueue(ctx, tx, outbox.Event{
			EventType: "test.lag", Payload: []byte(`{}`),
		})
	}); err != nil {
		t.Fatal(err)
	}
	// Backdate the row so it appears older than the lag threshold.
	if _, err := d.Exec(ctx,
		`UPDATE outbox SET created_at = NOW() - INTERVAL '1 hour' WHERE event_type = 'test.lag'`,
	); err != nil {
		t.Fatal(err)
	}
	c := outbox.NewChecker(d, outbox.WithMaxLag(time.Minute))
	err := c.Check(ctx)
	if err == nil {
		t.Fatal("Check should fail when oldest pending exceeds MaxLag")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != outbox.CodeBacklog {
		t.Errorf("err code = %v, want outbox_backlog", e)
	}
}

func TestChecker_CustomName(t *testing.T) {
	d := freshDB(t)
	c := outbox.NewChecker(d, outbox.WithCheckerName("orders-outbox"))
	if got := c.Name(); got != "orders-outbox" {
		t.Errorf("Name = %q, want orders-outbox", got)
	}
}
