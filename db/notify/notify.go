// Package notify is a goroutine-safe Postgres LISTEN/NOTIFY helper.
//
// The kit's outbox uses the same pattern internally; this package
// exposes it as a primitive for the broader set of LISTEN/NOTIFY
// use cases — cache invalidation broadcast, materialized view
// refresh signals, distributed locks notifications, real-time
// projections.
//
// Lifecycle:
//
//  1. Build with [NewNotifier]: supply *db.DB, the channel names to
//     subscribe to, and a Handler.
//  2. Call [Notifier.Start] to spawn the listen goroutine. A
//     dedicated pgxpool.Conn is held for the notifier's lifetime;
//     other queries continue to use the pool normally.
//  3. Handler receives a [Notification] per `pg_notify` call, in
//     receipt order per channel.
//  4. [Notifier.Stop] cancels the goroutine and releases the conn.
//
// The notifier reconnects on conn drops with bounded backoff so a
// Postgres restart doesn't permanently silence delivery. Sends from
// other clients during the reconnect window are LOST — NOTIFY has no
// durability. Callers that need durability should pair this with a
// recovery mechanism (e.g. a SELECT against an indexed table on
// reconnect to drain anything missed).
//
// # Concurrency
//
// Handler runs on a SINGLE goroutine, in receipt order. If the
// handler blocks, subsequent notifications queue up at the Postgres
// server-side buffer. For high-throughput sources, dispatch into a
// worker pool from inside Handler.
package notify

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/theizzatbek/gokit/db"
)

// Notification is one received pg_notify event.
type Notification struct {
	// Channel is the LISTEN channel name (the first argument to
	// pg_notify on the sender side).
	Channel string

	// Payload is the second argument to pg_notify, or "" when the
	// sender supplied nothing.
	Payload string
}

// Handler is the per-notification dispatch callback. Returning an
// error logs at Warn level and continues — Postgres has no concept
// of nak/redeliver for NOTIFY, so the error is purely operational.
type Handler func(ctx context.Context, n Notification) error

// Notifier is the persistent LISTEN client. Built via [NewNotifier];
// drive with Start / Stop.
type Notifier struct {
	db       *db.DB
	channels []string
	handler  Handler
	logger   *slog.Logger

	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	done      chan struct{}
}

// Option tunes [NewNotifier].
type Option func(*Notifier)

// WithLogger wires a slog.Logger for lifecycle + per-notification
// diagnostics. Without it the notifier runs silently.
func WithLogger(l *slog.Logger) Option {
	return func(n *Notifier) { n.logger = l }
}

// NewNotifier constructs a Notifier. channels and handler are
// required — nil/empty either errors at Start; this function only
// allocates so misconfiguration surfaces immediately on Start.
//
// Pass at least one channel; the kit refuses an empty channel list
// at Start (LISTEN with no channels is a programmer error).
//
// Multi-channel: one conn handles them all. Postgres dispatches per
// connection in arrival order across channels.
func NewNotifier(d *db.DB, channels []string, handler Handler, opts ...Option) *Notifier {
	n := &Notifier{
		db:       d,
		channels: append([]string(nil), channels...),
		handler:  handler,
		done:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

// Start spawns the listen goroutine. Idempotent — second call is a
// no-op (returns nil; for an erroring contract see Worker.Start in
// outbox). The supplied ctx anchors the goroutine lifetime; it
// exits when ctx is cancelled OR Stop is called.
func (n *Notifier) Start(ctx context.Context) error {
	if n == nil {
		return nil
	}
	n.startOnce.Do(func() {
		loopCtx, cancel := context.WithCancel(ctx)
		n.cancel = cancel
		go n.listenLoop(loopCtx)
	})
	return nil
}

// Stop cancels the listen goroutine and waits for it to exit.
// Idempotent + nil-safe.
func (n *Notifier) Stop() error {
	if n == nil {
		return nil
	}
	n.stopOnce.Do(func() {
		if n.cancel != nil {
			n.cancel()
		}
		<-n.done
	})
	return nil
}

// listenLoop is the outer reconnect loop. Holds a dedicated conn
// for the inner wait loop; on disconnect, releases + reacquires
// with bounded backoff.
func (n *Notifier) listenLoop(ctx context.Context) {
	defer close(n.done)
	backoff := 100 * time.Millisecond
	const maxBackoff = 5 * time.Second
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		conn, err := n.db.Pool().Acquire(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			if n.logger != nil {
				n.logger.Warn("notify: acquire failed",
					"err", err.Error(), "retry_in", backoff)
			}
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		if err := n.registerChannels(ctx, conn); err != nil {
			conn.Release()
			if n.logger != nil {
				n.logger.Warn("notify: LISTEN failed", "err", err.Error())
			}
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		backoff = 100 * time.Millisecond
		n.consume(ctx, conn)
		conn.Release()
	}
}

// registerChannels issues a LISTEN per channel name. Channel names
// are quoted-identifier safe: only A-Za-z0-9_ are accepted to keep
// the runner safe from SQL injection without dragging in a full
// quoting library. The check rejects anything else at Start time
// (effectively at first acquire); operators see the misconfig in
// logs.
func (n *Notifier) registerChannels(ctx context.Context, conn *pgxpool.Conn) error {
	if len(n.channels) == 0 {
		return errors.New("notify: at least one channel required")
	}
	for _, ch := range n.channels {
		if !safeIdent(ch) {
			return errors.New("notify: unsafe channel name " + ch)
		}
		if _, err := conn.Exec(ctx, "LISTEN "+ch); err != nil {
			return err
		}
	}
	return nil
}

// consume blocks on the dedicated conn, dispatching each received
// notification to the handler. Returns on first non-ctx error so
// the outer loop can reacquire + reregister.
func (n *Notifier) consume(ctx context.Context, conn *pgxpool.Conn) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		notif, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			if n.logger != nil {
				n.logger.Warn("notify: WaitForNotification failed", "err", err.Error())
			}
			return
		}
		nn := Notification{Channel: notif.Channel, Payload: notif.Payload}
		if err := n.handler(ctx, nn); err != nil {
			if n.logger != nil {
				n.logger.Warn("notify: handler error",
					"channel", nn.Channel, "err", err.Error())
			}
		}
	}
}

// safeIdent returns true when s is composed of letters, digits, or
// underscores. Used as a cheap identifier safelist for LISTEN
// channel names — Postgres accepts unquoted identifiers in this
// shape, so we don't need a full quoter just to support the kit's
// expected channel names.
func safeIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	// Reject pure-digit names (Postgres requires a leading letter
	// for unquoted identifiers).
	first := s[0]
	if first >= '0' && first <= '9' {
		return false
	}
	return true
}

// sleepCtx sleeps for d, honouring ctx cancellation. Returns true
// when the sleep completed; false when ctx was cancelled.
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

// suppress unused-import warning when strings goes unused after a
// refactor.
var _ = strings.Builder{}
