package inbox_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/inbox"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// ── ProcessBatch ──────────────────────────────────────────────────

func TestProcessBatch_AllNew(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	keys := []inbox.Key{
		{Consumer: "svc", EventID: "e1"},
		{Consumer: "svc", EventID: "e2"},
		{Consumer: "svc", EventID: "e3"},
	}

	var seenIdx []int
	outcomes, err := inbox.ProcessBatch(ctx, d, keys, func(tx *db.Tx, newIdx []int) error {
		seenIdx = append(seenIdx, newIdx...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 3 {
		t.Fatalf("len outcomes = %d, want 3", len(outcomes))
	}
	for i, o := range outcomes {
		if o != inbox.OutcomeProcessed {
			t.Errorf("outcomes[%d] = %v, want Processed", i, o)
		}
	}
	if len(seenIdx) != 3 {
		t.Errorf("fn saw %d new idx, want 3", len(seenIdx))
	}
}

func TestProcessBatch_PartialDuplicate(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	// Pre-insert e1 so it'll come back as duplicate.
	if _, err := inbox.MarkProcessed(ctx, d, inbox.Key{Consumer: "svc", EventID: "e1"}); err != nil {
		t.Fatal(err)
	}

	keys := []inbox.Key{
		{Consumer: "svc", EventID: "e1"}, // duplicate
		{Consumer: "svc", EventID: "e2"}, // new
		{Consumer: "svc", EventID: "e3"}, // new
	}
	var fnNewIdx []int
	outcomes, err := inbox.ProcessBatch(ctx, d, keys, func(tx *db.Tx, newIdx []int) error {
		fnNewIdx = append([]int(nil), newIdx...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0] != inbox.OutcomeDuplicate {
		t.Errorf("outcomes[0] = %v, want Duplicate", outcomes[0])
	}
	if outcomes[1] != inbox.OutcomeProcessed || outcomes[2] != inbox.OutcomeProcessed {
		t.Errorf("outcomes[1:3] = %v, want both Processed", outcomes[1:3])
	}
	if len(fnNewIdx) != 2 {
		t.Errorf("fn saw %d newIdx, want 2", len(fnNewIdx))
	}
	// Ensure fn newIdx are the right positions (1, 2 in the input).
	found := map[int]bool{}
	for _, i := range fnNewIdx {
		found[i] = true
	}
	if !found[1] || !found[2] {
		t.Errorf("fn newIdx = %v, want positions 1 and 2", fnNewIdx)
	}
}

func TestProcessBatch_FnErrorRollsBack(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	keys := []inbox.Key{
		{Consumer: "svc", EventID: "e1"},
		{Consumer: "svc", EventID: "e2"},
	}
	sentinel := errors.New("forced rollback")
	_, err := inbox.ProcessBatch(ctx, d, keys, func(tx *db.Tx, newIdx []int) error {
		return sentinel
	})
	if err == nil {
		t.Fatal("expected wrapped error")
	}
	// Verify inbox rows DON'T exist — rollback worked.
	for _, k := range keys {
		exists, err := inbox.Exists(ctx, d, k)
		if err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("key %v survived rollback", k)
		}
	}
}

func TestProcessBatch_EmptyErrors(t *testing.T) {
	d := freshDB(t)
	_, err := inbox.ProcessBatch(context.Background(), d, nil, nil)
	if err == nil {
		t.Fatal("expected error on empty batch")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != inbox.CodeBatchEmpty {
		t.Errorf("err = %+v, want CodeBatchEmpty", err)
	}
}

func TestProcessBatch_ValidatesEachKey(t *testing.T) {
	d := freshDB(t)
	bad := []inbox.Key{
		{Consumer: "svc", EventID: "ok"},
		{Consumer: "", EventID: "bad"},
	}
	_, err := inbox.ProcessBatch(context.Background(), d, bad, nil)
	if err == nil {
		t.Fatal("expected error for empty consumer")
	}
}

func TestProcessBatch_RepeatedKeyInBatch(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	// Same key twice in one batch — first position should be
	// Processed, second should be Duplicate.
	keys := []inbox.Key{
		{Consumer: "svc", EventID: "dup"},
		{Consumer: "svc", EventID: "dup"},
	}
	outcomes, err := inbox.ProcessBatch(ctx, d, keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0] != inbox.OutcomeProcessed {
		t.Errorf("outcomes[0] = %v, want Processed", outcomes[0])
	}
	if outcomes[1] != inbox.OutcomeDuplicate {
		t.Errorf("outcomes[1] = %v, want Duplicate", outcomes[1])
	}
}

// ── Exists ────────────────────────────────────────────────────────

func TestExists_NewKey(t *testing.T) {
	d := freshDB(t)
	exists, err := inbox.Exists(context.Background(), d,
		inbox.Key{Consumer: "svc", EventID: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("expected false for fresh key")
	}
}

func TestExists_AfterMarkProcessed(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	key := inbox.Key{Consumer: "svc", EventID: "marked"}
	if _, err := inbox.MarkProcessed(ctx, d, key); err != nil {
		t.Fatal(err)
	}
	exists, err := inbox.Exists(ctx, d, key)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected true after MarkProcessed")
	}
}

func TestExists_ValidatesKey(t *testing.T) {
	d := freshDB(t)
	_, err := inbox.Exists(context.Background(), d, inbox.Key{Consumer: "", EventID: "x"})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

// ── MarkProcessed ─────────────────────────────────────────────────

func TestMarkProcessed_FirstCallProcessed(t *testing.T) {
	d := freshDB(t)
	outcome, err := inbox.MarkProcessed(context.Background(), d,
		inbox.Key{Consumer: "svc", EventID: fmt.Sprintf("e-%d", 1)})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != inbox.OutcomeProcessed {
		t.Errorf("outcome = %v, want Processed", outcome)
	}
}

func TestMarkProcessed_SecondCallDuplicate(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	key := inbox.Key{Consumer: "svc", EventID: "twice"}
	if _, err := inbox.MarkProcessed(ctx, d, key); err != nil {
		t.Fatal(err)
	}
	outcome, err := inbox.MarkProcessed(ctx, d, key)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != inbox.OutcomeDuplicate {
		t.Errorf("outcome = %v, want Duplicate", outcome)
	}
}

func TestMarkProcessed_ValidatesKey(t *testing.T) {
	d := freshDB(t)
	_, err := inbox.MarkProcessed(context.Background(), d,
		inbox.Key{Consumer: "svc", EventID: ""})
	if err == nil {
		t.Fatal("expected validation error")
	}
}
