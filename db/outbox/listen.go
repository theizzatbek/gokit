package outbox

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// listenLoop holds a dedicated pgxpool connection that LISTENs on
// NotifyChannel and signals wake on every received notification.
// Reserves one slot from the underlying pool for the worker's
// lifetime — services should run with `db.Config.MaxConns >= 2`
// when listen is enabled (the default).
//
// The loop survives connection drops: a failed WaitForNotification
// triggers backoff + re-Acquire so a Postgres restart never
// permanently silences the wake-up path. Polling stays as the
// fallback for that window.
//
// Exits cleanly when ctx is cancelled (Worker.Stop). The dedicated
// conn is released back to the pool on exit.
func (w *Worker) listenLoop(ctx context.Context, wake chan<- struct{}) {
	defer close(w.listenExit)
	backoff := 100 * time.Millisecond
	const maxBackoff = 5 * time.Second
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		conn, err := w.db.Pool().Acquire(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			if w.logger != nil {
				w.logger.Warn("outbox: listen acquire failed",
					"err", err.Error(), "retry_in", backoff)
			}
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		if _, err := conn.Exec(ctx, "LISTEN "+NotifyChannel); err != nil {
			conn.Release()
			if w.logger != nil {
				w.logger.Warn("outbox: LISTEN failed", "err", err.Error())
			}
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		// Successful registration → reset backoff for next reconnect.
		backoff = 100 * time.Millisecond
		w.waitForNotifications(ctx, conn, wake)
		conn.Release()
	}
}

// waitForNotifications blocks on the dedicated conn, forwarding
// every received notification onto wake (non-blocking — the drain
// loop already has work queued if it doesn't read promptly).
// Returns on first non-ctx error so listenLoop can reacquire +
// re-register.
func (w *Worker) waitForNotifications(ctx context.Context, conn *pgxpool.Conn, wake chan<- struct{}) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			if w.logger != nil {
				w.logger.Warn("outbox: WaitForNotification failed", "err", err.Error())
			}
			return
		}
		w.metrics.recordWake()
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// sleepCtx sleeps for d, honouring ctx cancellation. Returns true
// when the sleep completed; false when ctx was cancelled mid-sleep.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// nextBackoff doubles d, capped at max.
func nextBackoff(d, max time.Duration) time.Duration {
	d *= 2
	if d > max {
		return max
	}
	return d
}
