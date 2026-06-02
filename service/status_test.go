package service

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestStatus_NilReceiver(t *testing.T) {
	t.Parallel()
	var s *Service[map[string]any, any]
	if got := s.Status(); got != (Status{}) {
		t.Errorf("nil Status() = %+v, want zero", got)
	}
}

func TestStatus_MinimalServiceAllFalse(t *testing.T) {
	t.Parallel()
	// Empty Config: every subsystem auto-detects to nil.
	svc, err := New[map[string]any, any](context.Background(), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	st := svc.Status()
	if st.DB || st.Auth || st.NATS || st.NATSMap || st.Redis || st.APIMap ||
		st.S3 || st.Outbox || st.Webhooks || st.RateLimiter ||
		st.OTel || st.Sentry || st.RefreshGC {
		t.Errorf("expected all subsystems off, got %+v", st)
	}
	if st.Cron != 0 {
		t.Errorf("Cron = %d, want 0", st.Cron)
	}
}

func TestStatus_CronCount(t *testing.T) {
	t.Parallel()
	jobs := []CronJob{
		{Name: "a", Schedule: "@every 1h", Fn: func(context.Context) error { return nil }},
		{Name: "b", Schedule: "@every 2h", Fn: func(context.Context) error { return nil }},
	}
	opts := []Option{}
	for _, j := range jobs {
		opts = append(opts, WithCron(j.Name, j.Schedule, j.Fn))
	}
	svc, err := New[map[string]any, any](context.Background(), Config{}, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	if got := svc.Status().Cron; got != 2 {
		t.Errorf("Cron = %d, want 2", got)
	}
}

func TestLogReady_EmitsServiceReadyLine(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	svc, err := New[map[string]any, any](context.Background(), Config{},
		WithLogger(logger),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	// Find the "service ready" line in the JSON-encoded log buffer.
	found := false
	for _, line := range bytesLines(buf.Bytes()) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		if msg, _ := m["msg"].(string); msg != "service ready" {
			continue
		}
		found = true
		// Spot-check a couple of expected fields.
		for _, k := range []string{"db", "auth", "nats", "natsmap", "redis", "otel", "sentry", "cron_jobs"} {
			if _, ok := m[k]; !ok {
				t.Errorf("missing %q in service-ready line", k)
			}
		}
		if v, _ := m["db"].(bool); v {
			t.Errorf("db = true on empty Config, want false")
		}
	}
	if !found {
		t.Errorf("'service ready' line not emitted; logs were: %s", buf.String())
	}
}

// bytesLines splits a buffer on '\n'. Local helper to avoid bringing
// in bufio for the test.
func bytesLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
