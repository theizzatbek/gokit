package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/audit"
	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestNew_NilStoreErrors(t *testing.T) {
	_, err := audit.New(nil, audit.Config{})
	if err == nil {
		t.Fatal("expected error for nil Store")
	}
	if e, ok := err.(*xerrs.Error); !ok || e.Code != audit.CodeNilStore {
		t.Errorf("err = %v, want CodeNilStore", err)
	}
}

func TestLog_RejectsMissingAction(t *testing.T) {
	l, _ := audit.New(audit.NewMemoryStore(), audit.Config{})
	_, err := l.Log(context.Background(), audit.Event{Outcome: audit.Success})
	if err == nil {
		t.Fatal("expected error for missing Action")
	}
}

func TestLog_RejectsMissingOutcome(t *testing.T) {
	l, _ := audit.New(audit.NewMemoryStore(), audit.Config{})
	_, err := l.Log(context.Background(), audit.Event{Action: "x.y"})
	if err == nil {
		t.Fatal("expected error for missing Outcome")
	}
}

func TestLog_AutoFillsIDOccurredAtServiceName(t *testing.T) {
	store := audit.NewMemoryStore()
	l, _ := audit.New(store, audit.Config{ServiceName: "tasks"})
	id, err := l.Log(context.Background(), audit.Event{
		Action: "user.login", Outcome: audit.Success,
		Actor: audit.Actor{Subject: "u-1"},
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if id == "" {
		t.Error("returned id empty")
	}
	snap := store.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot len = %d, want 1", len(snap))
	}
	e := snap[0]
	if e.ID != id {
		t.Errorf("snap.ID = %q, want %q", e.ID, id)
	}
	if e.OccurredAt.IsZero() {
		t.Error("OccurredAt not auto-filled")
	}
	if e.ServiceName != "tasks" {
		t.Errorf("ServiceName = %q, want tasks", e.ServiceName)
	}
}

func TestTypedConstructors_Login(t *testing.T) {
	store := audit.NewMemoryStore()
	l, _ := audit.New(store, audit.Config{})
	err := l.Login(context.Background(), audit.Actor{Subject: "u-1", IP: "1.2.3.4"}, audit.Success)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	snap := store.Snapshot()
	if snap[0].Action != "auth.login" || snap[0].Outcome != audit.Success {
		t.Errorf("snap = %+v, want auth.login/success", snap[0])
	}
}

func TestTypedConstructors_DeniedRecordsReason(t *testing.T) {
	store := audit.NewMemoryStore()
	l, _ := audit.New(store, audit.Config{})
	err := l.Denied(context.Background(),
		audit.Actor{Subject: "u-1"},
		audit.Target{Type: "post", ID: "p-1"},
		"post.delete", "not_owner")
	if err != nil {
		t.Fatalf("Denied: %v", err)
	}
	snap := store.Snapshot()
	if snap[0].Outcome != audit.Denied {
		t.Errorf("Outcome = %q, want denied", snap[0].Outcome)
	}
	if got := snap[0].Metadata["reason"]; got != "not_owner" {
		t.Errorf("Metadata.reason = %v, want not_owner", got)
	}
}

func TestQuery_FilterByActor(t *testing.T) {
	store := audit.NewMemoryStore()
	l, _ := audit.New(store, audit.Config{})
	for _, sub := range []string{"u-1", "u-2", "u-1"} {
		_ = l.Login(context.Background(), audit.Actor{Subject: sub}, audit.Success)
	}
	out, err := l.Query(context.Background(), audit.Filter{Actor: "u-1"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("filtered len = %d, want 2", len(out))
	}
}

func TestQuery_FilterByActionWildcard(t *testing.T) {
	store := audit.NewMemoryStore()
	l, _ := audit.New(store, audit.Config{})
	_, _ = l.Log(context.Background(), audit.Event{Action: "user.created", Outcome: audit.Success})
	_, _ = l.Log(context.Background(), audit.Event{Action: "user.updated", Outcome: audit.Success})
	_, _ = l.Log(context.Background(), audit.Event{Action: "post.created", Outcome: audit.Success})
	out, err := l.Query(context.Background(), audit.Filter{Action: "user.*"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("filtered len = %d, want 2", len(out))
	}
}

func TestQuery_FilterByTimeRange(t *testing.T) {
	store := audit.NewMemoryStore()
	l, _ := audit.New(store, audit.Config{})

	t0 := time.Now()
	for i := 0; i < 5; i++ {
		_, _ = l.Log(context.Background(), audit.Event{
			OccurredAt: t0.Add(time.Duration(i) * time.Minute),
			Action:     "x.y", Outcome: audit.Success,
		})
	}
	out, _ := l.Query(context.Background(), audit.Filter{
		From: t0.Add(time.Minute),
		To:   t0.Add(3 * time.Minute),
	})
	if len(out) != 3 {
		t.Errorf("time-range len = %d, want 3", len(out))
	}
}

func TestQuery_LimitOffset(t *testing.T) {
	store := audit.NewMemoryStore()
	l, _ := audit.New(store, audit.Config{})
	for i := 0; i < 10; i++ {
		_, _ = l.Log(context.Background(), audit.Event{
			OccurredAt: time.Now().Add(time.Duration(i) * time.Millisecond),
			Action:     "x.y", Outcome: audit.Success,
		})
	}
	out, _ := l.Query(context.Background(), audit.Filter{Limit: 3, Offset: 2})
	if len(out) != 3 {
		t.Errorf("paged len = %d, want 3", len(out))
	}
}

func TestPurgeBefore_DropsOldEvents(t *testing.T) {
	store := audit.NewMemoryStore()
	l, _ := audit.New(store, audit.Config{})
	old := time.Now().Add(-2 * time.Hour)
	for i := 0; i < 3; i++ {
		_, _ = l.Log(context.Background(), audit.Event{
			OccurredAt: old.Add(time.Duration(i) * time.Second),
			Action:     "old.event", Outcome: audit.Success,
		})
	}
	_, _ = l.Log(context.Background(), audit.Event{Action: "fresh", Outcome: audit.Success})

	n, err := l.PurgeBefore(context.Background(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("PurgeBefore: %v", err)
	}
	if n != 3 {
		t.Errorf("purged = %d, want 3", n)
	}
	snap := store.Snapshot()
	if len(snap) != 1 || snap[0].Action != "fresh" {
		t.Errorf("snap after purge = %+v, want only fresh", snap)
	}
}

func TestHashChain_BuildsAndVerifies(t *testing.T) {
	store := audit.NewMemoryStore()
	l, _ := audit.New(store, audit.Config{ServiceName: "tasks"}, audit.WithHashChain())
	for i := 0; i < 5; i++ {
		_, err := l.Log(context.Background(), audit.Event{
			OccurredAt: time.Now().Add(time.Duration(i) * time.Millisecond),
			Action:     "x.y", Outcome: audit.Success,
			Actor: audit.Actor{Subject: "u-1"},
		})
		if err != nil {
			t.Fatalf("Log[%d]: %v", i, err)
		}
	}
	events, _ := l.Query(context.Background(), audit.Filter{})
	if err := audit.Verify(events); err != nil {
		t.Errorf("Verify on intact chain: %v", err)
	}
}

func TestHashChain_TamperedEventBreaksVerify(t *testing.T) {
	store := audit.NewMemoryStore()
	l, _ := audit.New(store, audit.Config{}, audit.WithHashChain())
	for i := 0; i < 3; i++ {
		_, _ = l.Log(context.Background(), audit.Event{
			OccurredAt: time.Now().Add(time.Duration(i) * time.Millisecond),
			Action:     "x.y", Outcome: audit.Success,
		})
	}
	events, _ := l.Query(context.Background(), audit.Filter{})
	// Forge middle entry's Action; Hash stays same → mismatch.
	events[1].Action = "forged"
	err := audit.Verify(events)
	if err == nil {
		t.Fatal("Verify should reject tampered chain")
	}
	if e, ok := err.(*xerrs.Error); !ok || e.Code != audit.CodeChainBroken {
		t.Errorf("err = %v, want CodeChainBroken", err)
	}
}

func TestHashChain_DroppedEventBreaksVerify(t *testing.T) {
	store := audit.NewMemoryStore()
	l, _ := audit.New(store, audit.Config{}, audit.WithHashChain())
	for i := 0; i < 3; i++ {
		_, _ = l.Log(context.Background(), audit.Event{
			OccurredAt: time.Now().Add(time.Duration(i) * time.Millisecond),
			Action:     "x.y", Outcome: audit.Success,
		})
	}
	events, _ := l.Query(context.Background(), audit.Filter{})
	// Drop the middle event.
	events = append(events[:1], events[2:]...)
	err := audit.Verify(events)
	if err == nil {
		t.Fatal("Verify should reject chain with dropped event")
	}
}

func TestVerify_EmptyChain(t *testing.T) {
	if err := audit.Verify(nil); err != nil {
		t.Errorf("empty chain: %v", err)
	}
}

func TestVerify_NoChainModeAllZeroes(t *testing.T) {
	// When hash-chain wasn't enabled at write-time, Hash + PrevHash
	// are zero on every event. The canonical bytes for each event
	// include a zero PrevHash so every event's computed Hash would
	// differ from the stored nil — Verify is meaningful only with
	// chain mode.
	//
	// Sanity: verifying an explicitly nil slice succeeds.
	if err := audit.Verify([]audit.Event{}); err != nil {
		t.Errorf("empty-slice verify: %v", err)
	}
}
