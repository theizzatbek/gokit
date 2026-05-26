package natsmap

import (
	"context"
	"reflect"
	"strings"
	"testing"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
)

type tHandlerPayload struct {
	ID string `json:"id"`
}

func handlerNoop(ctx context.Context, m natsclient.Msg[tHandlerPayload]) error { return nil }

func TestEngine_LoadAndRegister_Basic(t *testing.T) {
	e := New()
	if err := e.LoadFile("testdata/events.yaml"); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	RegisterHandler[tHandlerPayload](e, "invoice_sender", handlerNoop)
	RegisterPublisher[tHandlerPayload](e, "orders.created")

	if got, want := len(e.subscribers), 1; got != want {
		t.Fatalf("subscribers: got %d want %d", got, want)
	}
	if got, want := len(e.publishers), 1; got != want {
		t.Fatalf("publishers: got %d want %d", got, want)
	}
	if reflect.TypeOf((*tHandlerPayload)(nil)).Elem() != e.handlerTypes["invoice_sender"] {
		t.Fatal("handler type not stored")
	}
	if reflect.TypeOf((*tHandlerPayload)(nil)).Elem() != e.publisherTypes["orders.created"] {
		t.Fatal("publisher type not stored")
	}
}

func TestEngine_RegisterDuplicateHandler_Panics(t *testing.T) {
	e := New()
	if err := e.LoadFile("testdata/events.yaml"); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	RegisterHandler[tHandlerPayload](e, "invoice_sender", handlerNoop)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate Register")
		}
		s := stringifyPanic(r)
		if !strings.Contains(s, CodeDuplicateSubscriber) {
			t.Fatalf("panic missing CodeDuplicateSubscriber: %v", r)
		}
	}()
	RegisterHandler[tHandlerPayload](e, "invoice_sender", handlerNoop)
}

func TestEngine_RegisterDuplicatePublisher_Panics(t *testing.T) {
	e := New()
	if err := e.LoadFile("testdata/events.yaml"); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	RegisterPublisher[tHandlerPayload](e, "orders.created")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate Register")
		}
		s := stringifyPanic(r)
		if !strings.Contains(s, CodeDuplicatePublisher) {
			t.Fatalf("panic missing CodeDuplicatePublisher: %v", r)
		}
	}()
	RegisterPublisher[tHandlerPayload](e, "orders.created")
}

func TestEngine_RegisterAfterBuild_Panics(t *testing.T) {
	e := New()
	if err := e.LoadFile("testdata/events.yaml"); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	RegisterHandler[tHandlerPayload](e, "invoice_sender", handlerNoop)
	RegisterPublisher[tHandlerPayload](e, "orders.created")
	e.built = true // simulate post-Build (avoid needing live NATS for unit test)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on post-Build Register")
		}
		s := stringifyPanic(r)
		if !strings.Contains(s, CodeAlreadyBuilt) {
			t.Fatalf("panic missing CodeAlreadyBuilt: %v", r)
		}
	}()
	RegisterHandler[tHandlerPayload](e, "another", handlerNoop)
}

func TestEngine_ValidateRequiresHandlerRegistration(t *testing.T) {
	e := New()
	if err := e.LoadFile("testdata/events.yaml"); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	// publisher registered; handler NOT registered.
	RegisterPublisher[tHandlerPayload](e, "orders.created")

	cfg := &rawConfig{Subscribers: e.subscribers, Publishers: e.publishers}
	err := cfg.validate(e.handlerNameSet(), e.publisherNameSet())
	if err == nil || !strings.Contains(err.Error(), CodeHandlerNotRegistered) {
		t.Fatalf("want CodeHandlerNotRegistered, got %v", err)
	}
}

func TestEngine_LoadBytesAppends(t *testing.T) {
	e := New()
	if err := e.LoadBytes([]byte("subscribers:\n  - {name: a, subject: x.y}\n")); err != nil {
		t.Fatalf("LoadBytes #1: %v", err)
	}
	if err := e.LoadBytes([]byte("publishers:\n  - {name: p, subject: x.y}\n")); err != nil {
		t.Fatalf("LoadBytes #2: %v", err)
	}
	if got, want := len(e.subscribers), 1; got != want {
		t.Fatalf("subscribers after merge: got %d want %d", got, want)
	}
	if got, want := len(e.publishers), 1; got != want {
		t.Fatalf("publishers after merge: got %d want %d", got, want)
	}
}

func stringifyPanic(v any) string {
	switch x := v.(type) {
	case error:
		return x.Error()
	case string:
		return x
	default:
		return ""
	}
}
