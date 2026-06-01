package webhooks

import (
	"context"
	"log/slog"
	"time"

	"github.com/theizzatbek/gokit/db"
)

// RetentionConfig drives RetentionWorker.
type RetentionConfig struct {
	DB        *db.DB
	TTL       time.Duration // default 30 * 24h
	Interval  time.Duration // default 1h
	BatchSize int           // default 1000
	Logger    *slog.Logger
}

// RetentionWorker periodically deletes `delivered` rows older than
// TTL. DLQ rows are intentionally NOT deleted — operator inspects.
type RetentionWorker struct {
	cfg    RetentionConfig
	stopCh chan struct{}
	doneCh chan struct{}
}

func NewRetentionWorker(cfg RetentionConfig) *RetentionWorker {
	if cfg.TTL == 0 {
		cfg.TTL = 30 * 24 * time.Hour
	}
	if cfg.Interval == 0 {
		cfg.Interval = time.Hour
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 1000
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &RetentionWorker{cfg: cfg, stopCh: make(chan struct{}), doneCh: make(chan struct{})}
}

func (r *RetentionWorker) Start(ctx context.Context) {
	go func() {
		defer close(r.doneCh)
		tick := time.NewTicker(r.cfg.Interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.stopCh:
				return
			case <-tick.C:
				r.sweep(ctx)
			}
		}
	}()
}

func (r *RetentionWorker) Stop(ctx context.Context) error {
	close(r.stopCh)
	select {
	case <-r.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *RetentionWorker) sweep(ctx context.Context) {
	cutoff := time.Now().Add(-r.cfg.TTL)
	tag, err := r.cfg.DB.Exec(ctx, `
		DELETE FROM webhook_deliveries
		WHERE id IN (
			SELECT id FROM webhook_deliveries
			WHERE status = 'delivered' AND delivered_at < $1
			LIMIT $2
		)
	`, cutoff, r.cfg.BatchSize)
	if err != nil {
		r.cfg.Logger.Error("webhooks: retention sweep failed", "err", err)
		return
	}
	if tag.RowsAffected() > 0 {
		r.cfg.Logger.Info("webhooks: retention swept", "rows", tag.RowsAffected())
	}
}
