package db

import (
	"errors"
	"testing"

	"github.com/theizzatbek/gokit/errs"
)

func TestNewBatch_QueueLen(t *testing.T) {
	b := NewBatch()
	if b.Len() != 0 {
		t.Errorf("empty batch len = %d, want 0", b.Len())
	}
	b.Queue("SELECT 1")
	b.Queue("SELECT 2")
	b.Queue("SELECT 3")
	if b.Len() != 3 {
		t.Errorf("batch len = %d, want 3", b.Len())
	}
}

func TestBatchResults_AdvanceOverrun(t *testing.T) {
	r := &BatchResults{remaining: 1}
	if err := r.advance(); err != nil {
		t.Fatalf("first advance: %v", err)
	}
	err := r.advance()
	if err == nil {
		t.Fatal("over-iteration should error")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != "db_batch_overrun" {
		t.Errorf("err = %v, want db_batch_overrun", err)
	}
}
