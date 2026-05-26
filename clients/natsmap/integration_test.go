package natsmap

import (
	"context"
	"flag"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
)

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
	c, err := tcnats.Run(ctx, "nats:2-alpine", testcontainers.WithCmd("-js"))
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

func newTestClient(t *testing.T) *natsclient.Client {
	t.Helper()
	if testing.Short() || testURL == "" {
		t.Skip("integration test — Docker required")
	}
	c, err := natsclient.Connect(context.Background(), natsclient.Config{URL: testURL, Name: "natsmap-test"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

type order struct {
	ID   string `json:"id"`
	Note string `json:"note,omitempty"`
}

func TestRuntime_PublishAndReceive(t *testing.T) {
	c := newTestClient(t)
	if err := c.EnsureStream(context.Background(), natsclient.StreamConfig{
		Name: "NATSMAPTEST", Subjects: []string{"natsmaptest.>"},
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	yaml := []byte(`subscribers:
  - name: receiver
    subject: natsmaptest.orders
    durable: receiver
publishers:
  - name: orders_out
    subject: natsmaptest.orders
`)
	eng := New()
	if err := eng.LoadBytes(yaml); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	var (
		mu       sync.Mutex
		received []order
	)
	RegisterHandler[order](eng, "receiver",
		func(ctx context.Context, m natsclient.Msg[order]) error {
			mu.Lock()
			received = append(received, m.Data)
			mu.Unlock()
			return nil
		})
	RegisterPublisher[order](eng, "orders_out")

	rt, err := eng.Build(context.Background(), c)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = rt.Drain() })

	if err := Publish[order](context.Background(), rt, "orders_out",
		order{ID: "o1", Note: "hello"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("did not receive published message within 3s")
		case <-time.After(50 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if got, want := received[0].ID, "o1"; got != want {
		t.Fatalf("payload mismatch: got %q want %q", got, want)
	}
}

func TestRuntime_BuildAggregatesValidationErrors(t *testing.T) {
	// Loading testdata/events.yaml without registering handler/publisher
	// should produce both CodeHandlerNotRegistered and CodePublisherNotRegistered.
	c := newTestClient(t)
	eng := New()
	if err := eng.LoadFile("testdata/events.yaml"); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	_, err := eng.Build(context.Background(), c)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), CodeHandlerNotRegistered) {
		t.Fatalf("want CodeHandlerNotRegistered in %v", err)
	}
	if !strings.Contains(err.Error(), CodePublisherNotRegistered) {
		t.Fatalf("want CodePublisherNotRegistered in %v", err)
	}
}

func TestRuntime_BuildTwiceFails(t *testing.T) {
	c := newTestClient(t)
	if err := c.EnsureStream(context.Background(), natsclient.StreamConfig{
		Name: "NATSMAPTEST2", Subjects: []string{"natsmaptest2.>"},
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	eng := New()
	if err := eng.LoadBytes([]byte(`publishers:
  - name: out
    subject: natsmaptest2.x
`)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	RegisterPublisher[order](eng, "out")
	if _, err := eng.Build(context.Background(), c); err != nil {
		t.Fatalf("first Build: %v", err)
	}
	if _, err := eng.Build(context.Background(), c); err == nil ||
		!strings.Contains(err.Error(), CodeAlreadyBuilt) {
		t.Fatalf("want CodeAlreadyBuilt, got %v", err)
	}
}
