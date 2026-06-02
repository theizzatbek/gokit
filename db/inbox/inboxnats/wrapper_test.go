package inboxnats

import (
	"context"
	"errors"
	"testing"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestDefaultEventIDFn_ReadsNatsMsgIDHeader(t *testing.T) {
	t.Parallel()
	got := DefaultEventIDFn(map[string][]string{
		NatsMsgIDHeader: {"abc-123"},
	}, "any.subject", 7)
	if got != "abc-123" {
		t.Errorf("got %q, want abc-123", got)
	}
}

func TestDefaultEventIDFn_MissingHeaderReturnsEmpty(t *testing.T) {
	t.Parallel()
	got := DefaultEventIDFn(map[string][]string{
		"X-Other": {"x"},
	}, "subject", 1)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDefaultEventIDFn_EmptyHeaderValueReturnsEmpty(t *testing.T) {
	t.Parallel()
	got := DefaultEventIDFn(map[string][]string{
		NatsMsgIDHeader: {},
	}, "subject", 1)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestWrap_MissingMessageIDShortCircuits(t *testing.T) {
	t.Parallel()
	// nil db is OK: the empty-id path returns BEFORE touching it.
	called := false
	wrapped := Wrap[string]("svc:test", nil,
		func(ctx context.Context, tx *db.Tx, m natsclient.Msg[string]) error {
			called = true
			return nil
		})

	err := wrapped(context.Background(), natsclient.Msg[string]{
		Subject: "x.y",
		Headers: map[string][]string{}, // no Nats-Msg-Id
	})
	if err == nil {
		t.Fatal("expected error for missing Nats-Msg-Id")
	}
	if called {
		t.Error("fn must not run when message id is missing")
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) {
		t.Fatalf("want *errs.Error, got %T (%v)", err, err)
	}
	if xe.Code != CodeMissingMessageID {
		t.Errorf("Code = %q, want %q", xe.Code, CodeMissingMessageID)
	}
}

func TestWrap_CustomEventIDFnOverride(t *testing.T) {
	t.Parallel()
	called := false
	resolver := func(headers map[string][]string, subject string, seq uint64) string {
		called = true
		return "from-resolver"
	}

	// Build options through the same path Wrap uses.
	o := &options{eventIDFn: DefaultEventIDFn}
	WithEventIDFn(resolver)(o)

	got := o.eventIDFn(nil, "x", 0)
	if got != "from-resolver" || !called {
		t.Errorf("override resolver: got %q called=%v", got, called)
	}
}
