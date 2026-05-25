package main

import (
	"context"
	"testing"
	"time"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
)

// TestExample_BuildClient_Smoke checks that the connect path compiles + runs
// against a running NATS server. Skipped unless NATS_URL is set or a server
// is reachable at the default port.
func TestExample_BuildClient_Smoke(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := natsclient.Connect(ctx, natsclient.Config{URL: "nats://127.0.0.1:4222", Name: "example-test"})
	if err != nil {
		t.Skip("no NATS server at localhost:4222 — skipping example smoke")
	}
	defer c.Close()
}
