package notify_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/theizzatbek/gokit/db/notify"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// ── Publish[T] ────────────────────────────────────────────────────

type publishPayload struct {
	Key string `json:"key"`
	N   int    `json:"n"`
}

func TestPublish_RoundTripJSON(t *testing.T) {
	d := freshDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan notify.Notification, 4)
	n := notify.NewNotifier(d, []string{"publish_round"},
		func(_ context.Context, nn notify.Notification) error {
			got <- nn
			return nil
		})
	if err := n.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })

	// Give the LISTEN a moment to land.
	time.Sleep(150 * time.Millisecond)

	if err := notify.Publish(ctx, d, "publish_round", publishPayload{Key: "k", N: 7}); err != nil {
		t.Fatal(err)
	}

	select {
	case nn := <-got:
		want := `{"key":"k","n":7}`
		if nn.Payload != want {
			t.Errorf("payload = %q, want %q", nn.Payload, want)
		}
		if nn.Channel != "publish_round" {
			t.Errorf("channel = %q", nn.Channel)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notification received")
	}
}

func TestPublishRaw_EmptyPayloadOK(t *testing.T) {
	d := freshDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan notify.Notification, 4)
	n := notify.NewNotifier(d, []string{"publish_empty"},
		func(_ context.Context, nn notify.Notification) error {
			got <- nn
			return nil
		})
	_ = n.Start(ctx)
	t.Cleanup(func() { _ = n.Stop() })
	time.Sleep(150 * time.Millisecond)

	if err := notify.PublishRaw(ctx, d, "publish_empty", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case nn := <-got:
		if nn.Payload != "" {
			t.Errorf("payload = %q, want empty", nn.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notification received")
	}
}

func TestPublish_InvalidChannelRejected(t *testing.T) {
	d := freshDB(t)
	err := notify.Publish(context.Background(), d, "bad name!", publishPayload{})
	if err == nil {
		t.Fatal("expected validation error")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != notify.CodeInvalidChannel {
		t.Errorf("err = %+v, want CodeInvalidChannel", err)
	}
}

func TestPublish_EmptyChannelRejected(t *testing.T) {
	d := freshDB(t)
	err := notify.Publish(context.Background(), d, "", publishPayload{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

// ── WithMetrics ───────────────────────────────────────────────────

func TestWithMetrics_RecordsNotifications(t *testing.T) {
	d := freshDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := prometheus.NewRegistry()
	n := notify.NewNotifier(d, []string{"metrics_ok"},
		func(_ context.Context, _ notify.Notification) error { return nil },
		notify.WithMetrics(reg),
	)
	_ = n.Start(ctx)
	t.Cleanup(func() { _ = n.Stop() })
	time.Sleep(150 * time.Millisecond)

	if err := notify.PublishRaw(ctx, d, "metrics_ok", "hi"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v := getCounterValue(t, reg, "notify_notifications_total", "outcome", "ok"); v == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("ok counter never reached 1")
}

func TestWithMetrics_HandlerErrorOutcome(t *testing.T) {
	d := freshDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := prometheus.NewRegistry()
	n := notify.NewNotifier(d, []string{"metrics_err"},
		func(_ context.Context, _ notify.Notification) error {
			return errors.New("nope")
		},
		notify.WithMetrics(reg),
	)
	_ = n.Start(ctx)
	t.Cleanup(func() { _ = n.Stop() })
	time.Sleep(150 * time.Millisecond)

	if err := notify.PublishRaw(ctx, d, "metrics_err", "x"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v := getCounterValue(t, reg, "notify_notifications_total", "outcome", "handler_error"); v == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("handler_error counter never reached 1")
}

// helpers

func getCounterValue(t *testing.T, reg *prometheus.Registry, name, labelKey, labelValue string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			ok := false
			for _, l := range m.GetLabel() {
				if l.GetName() == labelKey && l.GetValue() == labelValue {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
			if m.Counter != nil {
				return m.Counter.GetValue()
			}
		}
	}
	return 0
}

var _ = dto.MetricFamily{}
