package natsmap

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"gopkg.in/yaml.v3"
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

func TestWithEnv_MapWinsOverProcessEnv(t *testing.T) {
	t.Setenv("NATSMAP_TEST_KEY", "from-env")
	e := New(WithEnv(map[string]string{"NATSMAP_TEST_KEY": "from-map"}))
	yaml := []byte(`publishers:
  - name: p
    subject: ${NATSMAP_TEST_KEY}.created
`)
	if err := e.LoadBytes(yaml); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if got, want := e.publishers[0].Subject, "from-map.created"; got != want {
		t.Fatalf("Subject: got %q want %q (map should win)", got, want)
	}
}

func TestWithEnv_FallsBackToProcessEnv(t *testing.T) {
	t.Setenv("NATSMAP_TEST_KEY_X", "from-env-only")
	e := New(WithEnv(map[string]string{"OTHER_KEY": "y"}))
	yaml := []byte(`publishers:
  - name: p
    subject: ${NATSMAP_TEST_KEY_X}.created
`)
	if err := e.LoadBytes(yaml); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if got, want := e.publishers[0].Subject, "from-env-only.created"; got != want {
		t.Fatalf("Subject: got %q want %q (process env fallback)", got, want)
	}
}

func TestWithEnv_BothMissingReturnsCodeEnvVarUnset(t *testing.T) {
	os.Unsetenv("NATSMAP_TEST_KEY_MISSING")
	e := New(WithEnv(map[string]string{}))
	yaml := []byte(`publishers:
  - name: p
    subject: ${NATSMAP_TEST_KEY_MISSING}.created
`)
	err := e.LoadBytes(yaml)
	if err == nil || !strings.Contains(err.Error(), CodeEnvVarUnset) {
		t.Fatalf("want CodeEnvVarUnset, got %v", err)
	}
}

func TestRawStreamsBlock_UnmarshalAuto(t *testing.T) {
	var b rawStreamsBlock
	if err := yaml.Unmarshal([]byte(`auto`), &b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !b.Auto || len(b.List) != 0 {
		t.Fatalf("got %+v want Auto=true List=[]", b)
	}
}

func TestRawStreamsBlock_UnmarshalList(t *testing.T) {
	var b rawStreamsBlock
	if err := yaml.Unmarshal([]byte("- {name: X, subjects: [x.>]}\n"), &b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if b.Auto || len(b.List) != 1 || b.List[0].Name != "X" {
		t.Fatalf("got %+v", b)
	}
}

func TestRawStreamsBlock_UnmarshalGarbage(t *testing.T) {
	var b rawStreamsBlock
	err := yaml.Unmarshal([]byte("manual"), &b)
	if err == nil || !strings.Contains(err.Error(), "must be `auto`") {
		t.Fatalf("want scalar-error, got %v", err)
	}
}

func TestBuildStreamConfig_Defaults(t *testing.T) {
	s := &rawStream{Name: "X", Subjects: []string{"x.>"}}
	cfg, err := buildStreamConfig(s)
	if err != nil {
		t.Fatalf("buildStreamConfig: %v", err)
	}
	if cfg.Storage != natsclient.StorageFile {
		t.Fatalf("Storage: got %v want File", cfg.Storage)
	}
	if cfg.Retention != natsclient.RetentionLimits {
		t.Fatalf("Retention: got %v want Limits", cfg.Retention)
	}
}

func TestBuildStreamConfig_InvalidStorage(t *testing.T) {
	s := &rawStream{Name: "X", Subjects: []string{"x.>"}, Storage: "weird"}
	_, err := buildStreamConfig(s)
	if err == nil || !strings.Contains(err.Error(), CodeStreamInvalidStorage) {
		t.Fatalf("want CodeStreamInvalidStorage, got %v", err)
	}
}
