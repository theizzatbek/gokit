package outboxnats_test

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/db/outbox"
	"github.com/theizzatbek/gokit/db/outbox/outboxnats"
)

var testURL string

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		return 0
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
	// testcontainers NATS occasionally accepts TCP before the server
	// is fully ready and kicks the first connection with EOF; the
	// retry budget here absorbs that startup window without making
	// every other test wait.
	c, err := natsclient.Connect(context.Background(),
		natsclient.Config{
			URL:                testURL,
			Name:               "outboxnats-test",
			ConnectMaxRetries:  5,
			ConnectBackoffBase: 200 * time.Millisecond,
			ConnectBackoffMax:  2 * time.Second,
		})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(c.Close)
	if err := c.EnsureStream(context.Background(), natsclient.StreamConfig{
		Name: "OBN", Subjects: []string{"obn.>"},
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	return c
}

func TestNewPublisher_EndToEnd_DefaultName(t *testing.T) {
	c := newTestClient(t)

	eng := natsmap.New()
	if err := eng.LoadBytes([]byte(`subscribers:
  - name: sink
    subject: obn.events.created
    durable: sink
publishers:
  - name: events.created
    subject: obn.events.created
`)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	var (
		mu       sync.Mutex
		payloads [][]byte
		traceIDs []string
	)
	// Receive raw payload bytes via json.RawMessage — natsmap's
	// default codec is JSON, and RawMessage is a passthrough.
	natsmap.RegisterHandler[json.RawMessage](eng, "sink",
		func(ctx context.Context, m natsclient.Msg[json.RawMessage]) error {
			mu.Lock()
			payloads = append(payloads, append([]byte(nil), m.Data...))
			if v, ok := m.Headers["X-Trace-Id"]; ok && len(v) > 0 {
				traceIDs = append(traceIDs, v[0])
			} else {
				traceIDs = append(traceIDs, "")
			}
			mu.Unlock()
			return nil
		})
	// natsmap requires a registered Go type for declared publishers
	// even when we only call PublishRaw — register json.RawMessage to
	// satisfy Build's contract.
	natsmap.RegisterPublisher[json.RawMessage](eng, "events.created")

	rt, err := eng.Build(context.Background(), c)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = rt.Drain() })

	publisher := outboxnats.NewPublisher(rt)
	event := outbox.Event{
		ID:        uuid.New(),
		EventType: "events.created",
		Payload:   []byte(`{"id":"42"}`),
		Headers:   map[string][]string{"X-Trace-Id": {"abc"}},
	}
	if err := publisher(context.Background(), event); err != nil {
		t.Fatalf("publisher: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		n := len(payloads)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("did not receive event within 3s")
		case <-time.After(50 * time.Millisecond):
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if got, want := string(payloads[0]), `{"id":"42"}`; got != want {
		t.Errorf("payload = %q, want %q", got, want)
	}
	if got := traceIDs[0]; got != "abc" {
		t.Errorf("X-Trace-Id = %q, want %q", got, "abc")
	}
}

func TestNewPublisher_EndToEnd_NameOverride(t *testing.T) {
	c := newTestClient(t)

	eng := natsmap.New()
	if err := eng.LoadBytes([]byte(`subscribers:
  - name: sink
    subject: obn.bus.thing.happened
    durable: sink2
publishers:
  - name: prefixed.thing.happened
    subject: obn.bus.thing.happened
`)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	var (
		mu   sync.Mutex
		hits int
	)
	natsmap.RegisterHandler[json.RawMessage](eng, "sink",
		func(ctx context.Context, m natsclient.Msg[json.RawMessage]) error {
			mu.Lock()
			hits++
			mu.Unlock()
			return nil
		})
	natsmap.RegisterPublisher[json.RawMessage](eng, "prefixed.thing.happened")

	rt, err := eng.Build(context.Background(), c)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = rt.Drain() })

	publisher := outboxnats.NewPublisher(rt,
		outboxnats.WithPublisherNameFn(func(e outbox.Event) string {
			return "prefixed." + e.EventType
		}),
	)
	if err := publisher(context.Background(), outbox.Event{
		ID:        uuid.New(),
		EventType: "thing.happened",
		Payload:   []byte(`{}`),
	}); err != nil {
		t.Fatalf("publisher: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		n := hits
		mu.Unlock()
		if n >= 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("did not receive event within 3s")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestNewPublisher_UnknownPublisherSurfaces(t *testing.T) {
	c := newTestClient(t)

	eng := natsmap.New()
	if err := eng.LoadBytes([]byte(`publishers:
  - name: known
    subject: obn.known
`)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	natsmap.RegisterPublisher[json.RawMessage](eng, "known")
	rt, err := eng.Build(context.Background(), c)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = rt.Drain() })

	publisher := outboxnats.NewPublisher(rt)
	err = publisher(context.Background(), outbox.Event{
		ID:        uuid.New(),
		EventType: "unknown-name",
		Payload:   []byte(`{}`),
	})
	if err == nil {
		t.Fatal("expected error for unknown publisher")
	}
}
