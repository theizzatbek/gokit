package natsclient

import (
	"context"
	"flag"
	"os"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
)

// testURL is set by TestMain to the NATS URL exposed by the testcontainer.
// Empty in -short mode → integration tests skip.
var testURL string

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	c, err := tcnats.Run(ctx, "nats:2-alpine",
		// "-js" enables JetStream.
		testcontainers.WithCmd("-js"),
	)
	if err != nil {
		println("testcontainers nats start failed:", err.Error())
		return 1
	}
	defer testcontainers.TerminateContainer(c)
	endpoint, err := c.ConnectionString(ctx)
	if err != nil {
		println("nats endpoint:", err.Error())
		return 1
	}
	testURL = endpoint
	return m.Run()
}

// newTestClient connects to the test NATS server. Skips in -short mode.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	if testing.Short() || testURL == "" {
		t.Skip("integration test — Docker required")
	}
	c, err := Connect(context.Background(), Config{URL: testURL, Name: "test"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestConnect_Integration(t *testing.T) {
	c := newTestClient(t)
	if c.Conn() == nil || c.JetStream() == nil {
		t.Fatalf("Conn or JetStream nil after Connect")
	}
}
