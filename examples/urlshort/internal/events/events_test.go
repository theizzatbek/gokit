package events

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/theizzatbek/gokit/clients/natsmap"
)

func TestPublisher_LinkCreated_NilSafe(t *testing.T) {
	var p *Publisher
	// Must not panic on a nil receiver.
	p.LinkCreated(context.Background(), LinkCreated{Code: "x"})
}

func TestPublisher_LinkVisited_NilSafe(t *testing.T) {
	var p *Publisher
	// Must not panic on a nil receiver.
	p.LinkVisited(context.Background(), LinkVisited{Code: "x"})
}

func TestPublisher_LinkCreated_LogsOnPublishError(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	// Zero-value Runtime has nil publishers map → Publish returns
	// CodeUnknownPublisher (the natsmap zero-value contract used in
	// runtime_test.go's TestPublish_UnknownPublisher_Errors).
	rt := &natsmap.Runtime{}
	p := NewPublisher(rt, log)
	p.LinkCreated(context.Background(), LinkCreated{Code: "the-code"})
	out := buf.String()
	if !strings.Contains(out, "publish created failed") {
		t.Fatalf("missing failure log: %s", out)
	}
	if !strings.Contains(out, "the-code") {
		t.Fatalf("missing code in failure log: %s", out)
	}
}
