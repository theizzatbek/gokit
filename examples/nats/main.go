// Example: a minimal NATS-backed event flow — publish OrderCreated, subscribe
// from the same process, print, exit.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
)

type OrderCreated struct {
	ID     string `json:"id"`
	Amount int    `json:"amount"`
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := buildClient(ctx, logger)
	if err != nil {
		logger.Error("connect", "err", err)
		os.Exit(1)
	}
	defer c.Close()

	if err := c.EnsureStream(ctx, natsclient.StreamConfig{
		Name:     "EXAMPLE_ORDERS",
		Subjects: []string{"example.orders.>"},
		MaxAge:   time.Hour,
	}); err != nil {
		logger.Error("ensure stream", "err", err)
		os.Exit(1)
	}

	done := make(chan struct{})
	sub, err := natsclient.Subscribe[OrderCreated](ctx, c, "example.orders.created",
		func(_ context.Context, m natsclient.Msg[OrderCreated]) error {
			fmt.Printf("received: id=%s amount=%d seq=%d\n", m.Data.ID, m.Data.Amount, m.Sequence)
			close(done)
			return nil
		},
		natsclient.WithDurable("example-printer"),
	)
	if err != nil {
		logger.Error("subscribe", "err", err)
		os.Exit(1)
	}
	defer sub.Drain()

	pub := natsclient.NewPublisher[OrderCreated](c)
	if err := pub.Publish(ctx, "example.orders.created", OrderCreated{ID: "o-1", Amount: 42}); err != nil {
		logger.Error("publish", "err", err)
		os.Exit(1)
	}

	select {
	case <-done:
		logger.Info("example complete")
	case <-ctx.Done():
		logger.Error("timeout waiting for subscriber")
		os.Exit(1)
	}
}

func buildClient(ctx context.Context, logger *slog.Logger) (*natsclient.Client, error) {
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = "nats://localhost:4222"
	}
	return natsclient.Connect(ctx, natsclient.Config{
		URL:  url,
		Name: "example-nats",
	}, natsclient.WithLogger(logger))
}
