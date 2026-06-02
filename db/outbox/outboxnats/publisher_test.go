package outboxnats

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/theizzatbek/gokit/db/outbox"
	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestDefaultPublisherName_PassesEventTypeThrough(t *testing.T) {
	t.Parallel()
	e := outbox.Event{EventType: "urlshort.link.created"}
	if got := defaultPublisherName(e); got != "urlshort.link.created" {
		t.Errorf("default resolver = %q, want %q", got, e.EventType)
	}
}

func TestNewPublisher_NameResolverOverride(t *testing.T) {
	t.Parallel()
	calls := 0
	resolver := func(e outbox.Event) string {
		calls++
		return "bus." + e.EventType
	}

	// We can't run the PublishFn end-to-end without a *natsmap.Runtime
	// — integration_test.go covers that. Here we verify the resolver
	// option captures correctly into the closure by exposing the
	// options-build path through a helper.
	o := &options{publisherName: defaultPublisherName}
	WithPublisherNameFn(resolver)(o)

	e := outbox.Event{EventType: "x.y"}
	if got := o.publisherName(e); got != "bus.x.y" {
		t.Errorf("override resolver: got %q, want %q", got, "bus.x.y")
	}
	if calls != 1 {
		t.Errorf("override resolver call count = %d, want 1", calls)
	}
}

func TestPublishFn_EmptyNameShortCircuits(t *testing.T) {
	t.Parallel()
	// Build the PublishFn closure directly with a custom resolver that
	// yields "". NewPublisher's nil-Runtime path is not exercised here
	// — the empty-name guard runs BEFORE the natsmap call.
	fn := NewPublisher(nil, WithPublisherNameFn(func(outbox.Event) string {
		return ""
	}))

	err := fn(t.Context(), outbox.Event{
		ID:        uuid.New(),
		EventType: "anything",
	})
	if err == nil {
		t.Fatal("expected error on empty resolved name")
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) {
		t.Fatalf("want *errs.Error, got %T (%v)", err, err)
	}
	if xe.Code != CodeEmptyPublisherName {
		t.Errorf("Code = %q, want %q", xe.Code, CodeEmptyPublisherName)
	}
}

func TestPublishFn_EmptyEventTypeUnderDefault(t *testing.T) {
	t.Parallel()
	// Default resolver is identity on EventType — an empty EventType
	// must trigger the same short-circuit.
	fn := NewPublisher(nil)
	err := fn(t.Context(), outbox.Event{ID: uuid.New()})
	if err == nil {
		t.Fatal("expected error on empty EventType")
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) || xe.Code != CodeEmptyPublisherName {
		t.Errorf("err = %v, want CodeEmptyPublisherName", err)
	}
}
