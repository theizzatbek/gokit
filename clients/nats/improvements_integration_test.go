package natsclient

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type payload struct {
	ID string `json:"id"`
}

func ensureStream(t *testing.T, c *Client, name, subject string) {
	t.Helper()
	if err := c.EnsureStream(context.Background(), StreamConfig{
		Name:     name,
		Subjects: []string{subject},
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
}

// ── Handler resilience (DEF) ───────────────────────────────────────────

func TestSubscribe_PanicRecoveredAndRedelivered(t *testing.T) {
	c := newTestClient(t)
	ensureStream(t, c, "PANIC_S", "panic.>")

	var panicHit, calls atomic.Int32
	sub, err := Subscribe[payload](context.Background(), c, "panic.test",
		func(ctx context.Context, m Msg[payload]) error {
			n := calls.Add(1)
			if n == 1 {
				panic("boom")
			}
			return nil
		},
		WithDurable("d1"),
		WithMaxDeliver(3),
		WithAckWait(1*time.Second),
		WithBackoff(func(int) time.Duration { return 100 * time.Millisecond }),
		WithPanicHandler(func(any) { panicHit.Add(1) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Drain() })

	pub := NewPublisher[payload](c)
	if err := pub.Publish(context.Background(), "panic.test", payload{ID: "x"}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() >= 2 && panicHit.Load() >= 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("calls=%d panicHit=%d (want calls>=2, panicHit>=1)", calls.Load(), panicHit.Load())
}

func TestSubscribe_ClassifierTermsValidationErrors(t *testing.T) {
	c := newTestClient(t)
	ensureStream(t, c, "CLF_S", "clf.>")

	validation := errors.New("validation failure")
	var calls atomic.Int32
	sub, err := Subscribe[payload](context.Background(), c, "clf.test",
		func(ctx context.Context, m Msg[payload]) error {
			calls.Add(1)
			return validation
		},
		WithDurable("d2"),
		WithMaxDeliver(5),
		WithAckWait(1*time.Second),
		WithErrorClassifier(func(e error) AckAction {
			if errors.Is(e, validation) {
				return AckActTerm
			}
			return AckActNak
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Drain() })

	pub := NewPublisher[payload](c)
	_ = pub.Publish(context.Background(), "clf.test", payload{})

	// Wait 2s and confirm Term: total calls == 1 (no redelivery).
	time.Sleep(2 * time.Second)
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (classifier Term should prevent redelivery)", calls.Load())
	}
}

func TestSubscribe_AckProgressKeepsMessageAlive(t *testing.T) {
	c := newTestClient(t)
	ensureStream(t, c, "AP_S", "ap.>")

	var calls atomic.Int32
	sub, err := Subscribe[payload](context.Background(), c, "ap.test",
		func(ctx context.Context, m Msg[payload]) error {
			calls.Add(1)
			// Sleep longer than AckWait — without ack-progress
			// this would redeliver and bump calls > 1.
			time.Sleep(2 * time.Second)
			return nil
		},
		WithDurable("d3"),
		WithAckWait(500*time.Millisecond),
		WithAckProgress(150*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Drain() })

	pub := NewPublisher[payload](c)
	_ = pub.Publish(context.Background(), "ap.test", payload{})

	time.Sleep(3 * time.Second)
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (AckProgress should prevent redelivery)", got)
	}
}

// ── Pull mode (A) ──────────────────────────────────────────────────────

func TestPullSubscription_FetchAndAck(t *testing.T) {
	c := newTestClient(t)
	ensureStream(t, c, "PULL_S", "pull.>")

	pub := NewPublisher[payload](c)
	for i := 0; i < 3; i++ {
		if err := pub.Publish(context.Background(), "pull.test", payload{ID: "x"}); err != nil {
			t.Fatal(err)
		}
	}

	ps, err := NewPullSubscription[payload](c, "pull.test", WithDurable("dPull"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Drain() })

	batch, err := ps.Fetch(context.Background(), 5, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 3 {
		t.Errorf("len(batch) = %d, want 3", len(batch))
	}
	for _, m := range batch {
		if err := m.Ack(); err != nil {
			t.Errorf("Ack: %v", err)
		}
	}
}

// ── Request/Reply (B) ──────────────────────────────────────────────────

func TestRequestReply_RoundTrip(t *testing.T) {
	c := newTestClient(t)

	rep, err := Reply[payload, payload](context.Background(), c, "rpc.echo", "",
		func(ctx context.Context, in payload) (payload, error) {
			return payload{ID: in.ID + "-echoed"}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rep.Drain() })

	got, err := Request[payload, payload](context.Background(), c, "rpc.echo",
		payload{ID: "x"}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "x-echoed" {
		t.Errorf("got = %v, want id=x-echoed", got)
	}
}

func TestRequest_TimeoutMaps(t *testing.T) {
	c := newTestClient(t)
	_, err := Request[payload, payload](context.Background(), c, "rpc.nobody",
		payload{}, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// ── KV (C) ─────────────────────────────────────────────────────────────

func TestKV_PutGetRoundTrip(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.EnsureKVBucket(context.Background(), KVConfig{
		Bucket:  "cfg",
		History: 3,
	}); err != nil {
		t.Fatal(err)
	}
	kv, err := NewKV[payload](c, "cfg")
	if err != nil {
		t.Fatal(err)
	}
	rev, err := kv.Put(context.Background(), "k1", payload{ID: "v"})
	if err != nil {
		t.Fatal(err)
	}
	if rev != 1 {
		t.Errorf("first put rev = %d, want 1", rev)
	}
	got, gotRev, err := kv.Get(context.Background(), "k1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "v" || gotRev != rev {
		t.Errorf("Get = %v rev=%d, want v rev=%d", got, gotRev, rev)
	}
}

func TestKV_GetMissReturnsNotFound(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.EnsureKVBucket(context.Background(), KVConfig{Bucket: "miss"}); err != nil {
		t.Fatal(err)
	}
	kv, _ := NewKV[payload](c, "miss")
	if _, _, err := kv.Get(context.Background(), "missing"); err == nil {
		t.Error("expected NotFound on Get missing")
	}
}

func TestKV_UpdateCASConflict(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.EnsureKVBucket(context.Background(), KVConfig{Bucket: "cas"}); err != nil {
		t.Fatal(err)
	}
	kv, _ := NewKV[payload](c, "cas")
	_, _ = kv.Put(context.Background(), "k", payload{ID: "v1"})
	// Stale revision triggers Conflict.
	if _, err := kv.Update(context.Background(), "k", payload{ID: "v2"}, 999); err == nil {
		t.Error("expected CAS conflict on stale revision")
	}
}
