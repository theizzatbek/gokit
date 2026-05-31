package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// gcLoop runs the retention sweep at gcInterval cadence until ctx
// is cancelled. Each pass deletes rows where
// `published_at < NOW() - retention`. retention <= 0 disables the
// loop entirely (it returns immediately) — used as the default for
// callers who don't pass [WithRetention].
//
// The DELETE is a single statement; PG locks rows briefly, the
// partial index keeps it index-only on the unpublished side. No
// FOR UPDATE: GC competes with the drain SELECT only on already-
// published rows the drain doesn't care about.
func (w *Worker) gcLoop(ctx context.Context) {
	defer close(w.gcExit)
	if w.retention <= 0 {
		return
	}
	// Initial sweep on Start so a long-running deployment with a
	// stale outbox doesn't have to wait one whole interval before
	// the first cleanup.
	w.runGC(ctx)
	ticker := time.NewTicker(w.gcInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runGC(ctx)
		}
	}
}

func (w *Worker) runGC(ctx context.Context) {
	const sql = `
		DELETE FROM outbox
		WHERE published_at IS NOT NULL
		  AND published_at < NOW() - $1::interval
	`
	interval := fmt.Sprintf("%d microseconds", w.retention.Microseconds())
	tag, err := w.db.Exec(ctx, sql, interval)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		if w.logger != nil {
			w.logger.Warn("outbox: gc delete failed", "err", err.Error())
		}
		return
	}
	n := tag.RowsAffected()
	w.metrics.recordGC(n)
	if w.logger != nil && n > 0 {
		w.logger.Debug("outbox: gc swept", "deleted", n)
	}
}
