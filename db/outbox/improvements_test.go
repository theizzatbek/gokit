package outbox_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/outbox"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// enqueueAndGetID inserts an event and returns its generated UUID.
func enqueueAndGetID(t *testing.T, d *db.DB, evt outbox.Event) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	if evt.Payload == nil {
		evt.Payload = []byte(`{}`) // schema's payload column is NOT NULL
	}
	var id uuid.UUID
	if err := d.Tx(ctx, func(tx *db.Tx) error {
		return outbox.Enqueue(ctx, tx, evt)
	}); err != nil {
		t.Fatal(err)
	}
	// Re-fetch the ID — Enqueue doesn't expose it.
	if err := d.QueryRow(ctx,
		`SELECT id FROM outbox WHERE event_type=$1 ORDER BY created_at DESC LIMIT 1`,
		evt.EventType,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// ── RetryNow ──────────────────────────────────────────────────────

func TestRetryNow_HappyPath(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	id := enqueueAndGetID(t, d, outbox.Event{EventType: "test.retrynow"})

	// Push next_retry_at far into the future.
	if _, err := d.Exec(ctx,
		`UPDATE outbox SET next_retry_at = NOW() + INTERVAL '1 hour' WHERE id = $1`, id,
	); err != nil {
		t.Fatal(err)
	}

	if err := outbox.RetryNow(ctx, d, id); err != nil {
		t.Fatal(err)
	}

	var nr time.Time
	if err := d.QueryRow(ctx,
		`SELECT next_retry_at FROM outbox WHERE id = $1`, id,
	).Scan(&nr); err != nil {
		t.Fatal(err)
	}
	if time.Until(nr) > 500*time.Millisecond {
		t.Errorf("next_retry_at = %v, want ~now", nr)
	}
}

func TestRetryNow_NotFound(t *testing.T) {
	d := freshDB(t)
	err := outbox.RetryNow(context.Background(), d, uuid.New())
	if err == nil {
		t.Fatal("expected not-found error")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != outbox.CodeOpNotFound {
		t.Errorf("err = %+v, want CodeOpNotFound", err)
	}
}

func TestRetryNow_AlreadyPublishedNotFound(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	id := enqueueAndGetID(t, d, outbox.Event{EventType: "test.alreadypub"})
	if _, err := d.Exec(ctx, `UPDATE outbox SET published_at = NOW() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	err := outbox.RetryNow(ctx, d, id)
	if err == nil {
		t.Fatal("expected not-found for published row")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != outbox.CodeOpNotFound {
		t.Errorf("err = %+v, want CodeOpNotFound", err)
	}
}

// ── Replay ────────────────────────────────────────────────────────

func TestReplay_HappyPath(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	id1 := enqueueAndGetID(t, d, outbox.Event{EventType: "test.replay1"})
	id2 := enqueueAndGetID(t, d, outbox.Event{EventType: "test.replay2"})
	// Mark both published.
	if _, err := d.Exec(ctx, `UPDATE outbox SET published_at = NOW(), attempts = 3 WHERE id = ANY($1::uuid[])`,
		[]uuid.UUID{id1, id2}); err != nil {
		t.Fatal(err)
	}

	n, err := outbox.Replay(ctx, d, id1, id2)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}

	var pub *time.Time
	var attempts int
	if err := d.QueryRow(ctx,
		`SELECT published_at, attempts FROM outbox WHERE id = $1`, id1,
	).Scan(&pub, &attempts); err != nil {
		t.Fatal(err)
	}
	if pub != nil {
		t.Errorf("published_at = %v, want NULL", pub)
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, want 0", attempts)
	}
}

func TestReplay_EmptyIsNoOp(t *testing.T) {
	d := freshDB(t)
	n, err := outbox.Replay(context.Background(), d)
	if err != nil || n != 0 {
		t.Errorf("n=%d err=%v, want 0/nil", n, err)
	}
}

func TestReplay_MissingIDsSkipped(t *testing.T) {
	d := freshDB(t)
	n, err := outbox.Replay(context.Background(), d, uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

// ── ResetAttempts ─────────────────────────────────────────────────

func TestResetAttempts_HappyPath(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	id := enqueueAndGetID(t, d, outbox.Event{EventType: "test.reset"})
	if _, err := d.Exec(ctx,
		`UPDATE outbox SET attempts = 5, last_error = 'oops', next_retry_at = NOW() + INTERVAL '1 hour' WHERE id = $1`,
		id,
	); err != nil {
		t.Fatal(err)
	}

	if err := outbox.ResetAttempts(ctx, d, id); err != nil {
		t.Fatal(err)
	}

	var attempts int
	var lastErr *string
	if err := d.QueryRow(ctx,
		`SELECT attempts, last_error FROM outbox WHERE id = $1`, id,
	).Scan(&attempts, &lastErr); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || lastErr != nil {
		t.Errorf("attempts=%d, last_error=%v, want 0/nil", attempts, lastErr)
	}
}

func TestResetAttempts_NotFoundForPublished(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	id := enqueueAndGetID(t, d, outbox.Event{EventType: "test.reset.pub"})
	if _, err := d.Exec(ctx, `UPDATE outbox SET published_at = NOW() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	err := outbox.ResetAttempts(ctx, d, id)
	if err == nil {
		t.Fatal("expected not-found")
	}
}

// ── GatherStats ───────────────────────────────────────────────────

func TestGatherStats_EmptyTable(t *testing.T) {
	d := freshDB(t)
	s, err := outbox.GatherStats(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if s.Pending != 0 || s.Eligible != 0 || s.Failed != 0 || s.Published1m != 0 {
		t.Errorf("stats not zero: %+v", s)
	}
	if !s.OldestPending.IsZero() {
		t.Errorf("OldestPending = %v, want zero", s.OldestPending)
	}
}

func TestGatherStats_MixedPopulation(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	// 3 pending (one failed-and-eligible, one failed-and-future-backed-off, one fresh).
	idPending := enqueueAndGetID(t, d, outbox.Event{EventType: "p"})
	idFailedNow := enqueueAndGetID(t, d, outbox.Event{EventType: "fn"})
	idFailedBackedOff := enqueueAndGetID(t, d, outbox.Event{EventType: "fb"})

	if _, err := d.Exec(ctx, `UPDATE outbox SET attempts=1 WHERE id=$1`, idFailedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `UPDATE outbox SET attempts=1, next_retry_at=NOW()+INTERVAL '10 minutes' WHERE id=$1`, idFailedBackedOff); err != nil {
		t.Fatal(err)
	}

	// 1 published (recent).
	idDone := enqueueAndGetID(t, d, outbox.Event{EventType: "done"})
	if _, err := d.Exec(ctx, `UPDATE outbox SET published_at=NOW() WHERE id=$1`, idDone); err != nil {
		t.Fatal(err)
	}

	s, err := outbox.GatherStats(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if s.Pending != 3 {
		t.Errorf("Pending = %d, want 3", s.Pending)
	}
	if s.Eligible != 2 {
		t.Errorf("Eligible = %d, want 2", s.Eligible)
	}
	if s.Failed != 2 {
		t.Errorf("Failed = %d, want 2", s.Failed)
	}
	if s.Published1m != 1 {
		t.Errorf("Published1m = %d, want 1", s.Published1m)
	}
	if s.OldestPending.IsZero() {
		t.Errorf("OldestPending unset")
	}

	// keep id references alive
	_ = idPending
}

// ── ListPending / ListDead ────────────────────────────────────────

func TestListPending_OrderAndLimit(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	// 5 events, all eligible.
	for i := 0; i < 5; i++ {
		_ = enqueueAndGetID(t, d, outbox.Event{EventType: "lp"})
	}

	events, err := outbox.ListPending(ctx, d, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Errorf("got %d, want 3", len(events))
	}
}

func TestListPending_ZeroLimitReturnsNil(t *testing.T) {
	d := freshDB(t)
	events, err := outbox.ListPending(context.Background(), d, 0)
	if err != nil || events != nil {
		t.Errorf("got %v / %v, want nil/nil", events, err)
	}
}

func TestListDead_FiltersByAttempts(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	idAlive := enqueueAndGetID(t, d, outbox.Event{EventType: "ld.alive"})
	idDead1 := enqueueAndGetID(t, d, outbox.Event{EventType: "ld.dead1"})
	idDead2 := enqueueAndGetID(t, d, outbox.Event{EventType: "ld.dead2"})

	if _, err := d.Exec(ctx, `UPDATE outbox SET attempts=2 WHERE id=$1`, idAlive); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `UPDATE outbox SET attempts=5 WHERE id=ANY($1::uuid[])`,
		[]uuid.UUID{idDead1, idDead2}); err != nil {
		t.Fatal(err)
	}

	events, err := outbox.ListDead(ctx, d, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Errorf("got %d dead, want 2", len(events))
	}
}

func TestListDead_NoMaxAttemptsReturnsNil(t *testing.T) {
	d := freshDB(t)
	events, err := outbox.ListDead(context.Background(), d, 10, 0)
	if err != nil || events != nil {
		t.Errorf("got %v / %v, want nil/nil", events, err)
	}
}

// ── Per-event-type maxAttempts override ───────────────────────────

func TestWithEventTypeMaxAttempts_OverridesGlobal(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	// Enqueue one of each type.
	if err := d.Tx(ctx, func(tx *db.Tx) error {
		if err := outbox.Enqueue(ctx, tx, outbox.Event{EventType: "loud", Payload: []byte(`{}`)}); err != nil {
			return err
		}
		return outbox.Enqueue(ctx, tx, outbox.Event{EventType: "quiet", Payload: []byte(`{}`)})
	}); err != nil {
		t.Fatal(err)
	}

	var loudAttempts, quietAttempts atomic.Int32

	w, err := outbox.NewWorker(d,
		func(_ context.Context, e outbox.Event) error {
			if e.EventType == "loud" {
				loudAttempts.Add(1)
			} else {
				quietAttempts.Add(1)
			}
			return errors.New("nope")
		},
		outbox.WithInterval(15*time.Millisecond),
		outbox.WithMaxAttempts(5), // global default
		outbox.WithEventTypeMaxAttempts(map[string]int{
			"quiet": 1, // tighter cap
		}),
		outbox.WithBackoff(0, 0), // no backoff in test
	)
	if err != nil {
		t.Fatal(err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := w.Start(loopCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop() })

	// Let the worker burn through both event types.
	waitFor(t, 3*time.Second, func() bool {
		return loudAttempts.Load() >= 5 && quietAttempts.Load() >= 1
	})
	time.Sleep(200 * time.Millisecond) // grace for additional attempts

	loud := loudAttempts.Load()
	quiet := quietAttempts.Load()
	if loud != 5 {
		t.Errorf("loud attempts = %d, want exactly 5 (global cap)", loud)
	}
	if quiet != 1 {
		t.Errorf("quiet attempts = %d, want exactly 1 (per-type override)", quiet)
	}
}

// silence unused
var _ = sync.Mutex{}
