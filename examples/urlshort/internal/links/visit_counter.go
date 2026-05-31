package links

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/db"

	"github.com/theizzatbek/gokit/examples/urlshort/internal/events"
)

// VisitCounter is the NATS subscriber that persists visit counts.
//
// Each Handle call accumulates the event into an in-memory map keyed
// by code; a background goroutine flushes the map periodically (every
// flushInterval) OR when it reaches batchTrigger entries. The flush
// runs ONE SQL statement: an UPDATE … FROM (VALUES …) that bumps every
// affected row in a single round-trip. For a hot-code endpoint this
// converts thousands of single-row UPDATEs into one batched write per
// second.
//
// Delivery semantics: natsmap auto-acks each event when Handle
// returns nil. The buffer therefore lives between "event ack'd" and
// "row updated" — a crash inside that window loses up to one
// flushInterval worth of clicks. For click-counter analytics this is
// an acceptable trade (vs. the complexity of manual-ack JetStream
// subscriptions); if your domain needs strict at-least-once,
// subscribe via natsclient directly and Ack only after a successful
// flush.
type VisitCounter struct {
	db  *db.DB
	log *slog.Logger

	mu       sync.Mutex
	pending  map[string]*visitAgg
	maxBatch int

	flushCh  chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

type visitAgg struct {
	delta  int64
	lastTS time.Time
}

const (
	// flushInterval bounds the in-memory buffer's age. Lower = less
	// loss on crash, more DB writes. 1s is a sane default for click
	// counters: 99.9% of clicks land within this window.
	flushInterval = time.Second

	// batchTrigger forces a flush as soon as the pending map holds
	// this many distinct codes. Caps memory + ensures a sudden burst
	// drains rather than waiting for the ticker.
	batchTrigger = 1000
)

// NewVisitCounter starts the background flush goroutine. Close stops
// it (one final flush included). The returned instance is safe to
// concurrently invoke Handle on from many subscriber goroutines.
func NewVisitCounter(d *db.DB, log *slog.Logger) *VisitCounter {
	vc := &VisitCounter{
		db:       d,
		log:      log,
		pending:  make(map[string]*visitAgg, batchTrigger),
		maxBatch: batchTrigger,
		flushCh:  make(chan struct{}, 1),
		doneCh:   make(chan struct{}),
	}
	vc.wg.Add(1)
	go vc.flushLoop()
	return vc
}

// Close stops the flush loop after one final drain. Idempotent.
// Register via service.OnShutdown so in-flight events make it to
// Postgres before the DB pool tears down.
func (vc *VisitCounter) Close() error {
	vc.stopOnce.Do(func() { close(vc.doneCh) })
	vc.wg.Wait()
	return nil
}

// Handle is the natsmap-compatible signature. Accumulates the event
// into the pending map; the goroutine takes care of the SQL.
func (vc *VisitCounter) Handle(_ context.Context, m natsclient.Msg[events.LinkVisited]) error {
	e := m.Data

	vc.mu.Lock()
	agg, ok := vc.pending[e.Code]
	if !ok {
		agg = &visitAgg{}
		vc.pending[e.Code] = agg
	}
	agg.delta++
	if e.VisitedAt.After(agg.lastTS) {
		agg.lastTS = e.VisitedAt
	}
	full := len(vc.pending) >= vc.maxBatch
	vc.mu.Unlock()

	if full {
		// Best-effort kick — if the channel is full a tick will
		// pick it up.
		select {
		case vc.flushCh <- struct{}{}:
		default:
		}
	}
	return nil
}

func (vc *VisitCounter) flushLoop() {
	defer vc.wg.Done()
	t := time.NewTicker(flushInterval)
	defer t.Stop()
	for {
		select {
		case <-vc.doneCh:
			vc.flush(context.Background())
			return
		case <-t.C:
			vc.flush(context.Background())
		case <-vc.flushCh:
			vc.flush(context.Background())
		}
	}
}

// flush swaps the pending map and runs one batched UPDATE per call.
// The map swap happens under the mutex; the network round-trip and
// SQL building happen outside it so Handle remains lock-free for
// new events arriving during the flush.
func (vc *VisitCounter) flush(ctx context.Context) {
	vc.mu.Lock()
	if len(vc.pending) == 0 {
		vc.mu.Unlock()
		return
	}
	batch := vc.pending
	vc.pending = make(map[string]*visitAgg, vc.maxBatch)
	vc.mu.Unlock()

	sql, args := buildVisitUpdate(batch)
	if _, err := vc.db.Exec(ctx, sql, args...); err != nil {
		// Best effort: log + drop. We've already ack'd these events
		// upstream (natsmap auto-ack on nil Handle return), so
		// retrying here would force us to re-add to the buffer at
		// the risk of unbounded growth. Production deployments that
		// can't tolerate the loss should switch to a manual-ack
		// JetStream subscription.
		if vc.log != nil {
			vc.log.Warn("urlshort visit counter: batch update failed",
				"batch_codes", len(batch), "err", err.Error())
		}
		return
	}
	if vc.log != nil {
		var total int64
		for _, a := range batch {
			total += a.delta
		}
		vc.log.Debug("urlshort visit counter: flushed",
			"codes", len(batch), "visits", total)
	}
}

// buildVisitUpdate produces a single UPDATE … FROM (VALUES …) query
// that bumps every (code, delta, lastTS) tuple in batch. Generated as
// a raw $-numbered SQL string instead of squirrel because squirrel
// doesn't have a first-class VALUES-list builder.
//
// Example output for two entries:
//
//	UPDATE links AS l
//	SET visit_count = l.visit_count + v.delta,
//	    last_visited_at = greatest(
//	        coalesce(l.last_visited_at, 'epoch'::timestamptz),
//	        v.ts)
//	FROM (VALUES
//	    ($1::text, $2::bigint, $3::timestamptz),
//	    ($4, $5, $6)
//	) AS v(code, delta, ts)
//	WHERE l.code = v.code;
func buildVisitUpdate(batch map[string]*visitAgg) (string, []any) {
	var b strings.Builder
	b.WriteString(`UPDATE links AS l SET visit_count = l.visit_count + v.delta, ` +
		`last_visited_at = greatest(coalesce(l.last_visited_at, 'epoch'::timestamptz), v.ts) ` +
		`FROM (VALUES `)
	args := make([]any, 0, len(batch)*3)
	first := true
	i := 1
	for code, agg := range batch {
		if !first {
			b.WriteByte(',')
		}
		first = false
		if i == 1 {
			b.WriteString(`($` + strconv.Itoa(i) + `::text,$` + strconv.Itoa(i+1) + `::bigint,$` + strconv.Itoa(i+2) + `::timestamptz)`)
		} else {
			b.WriteString(`($` + strconv.Itoa(i) + `,$` + strconv.Itoa(i+1) + `,$` + strconv.Itoa(i+2) + `)`)
		}
		args = append(args, code, agg.delta, agg.lastTS)
		i += 3
	}
	b.WriteString(`) AS v(code, delta, ts) WHERE l.code = v.code`)
	return b.String(), args
}
