package natsmap

import (
	"context"
	"os"
	"reflect"
	"sort"
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

func TestDeriveStreamsFromSubjects(t *testing.T) {
	cases := []struct {
		name string
		subs []rawSubscriber
		pubs []rawPublisher
		want []rawStream
	}{
		{
			name: "single segment grouping",
			subs: []rawSubscriber{{Name: "a", Subject: "orders.created"}},
			pubs: []rawPublisher{{Name: "p", Subject: "orders.shipped"}},
			want: []rawStream{{Name: "ORDERS", Subjects: []string{"orders.>"}}},
		},
		{
			name: "wildcard subjects",
			pubs: []rawPublisher{{Name: "p", Subject: "users.>"}},
			want: []rawStream{{Name: "USERS", Subjects: []string{"users.>"}}},
		},
		{
			name: "no dots fallback",
			pubs: []rawPublisher{{Name: "p", Subject: "events"}},
			want: []rawStream{{Name: "EVENTS", Subjects: []string{"events"}}},
		},
		{
			name: "multiple groups",
			subs: []rawSubscriber{{Name: "a", Subject: "orders.x"}, {Name: "b", Subject: "users.y"}},
			want: []rawStream{
				{Name: "ORDERS", Subjects: []string{"orders.>"}},
				{Name: "USERS", Subjects: []string{"users.>"}},
			},
		},
		{
			name: "empty input",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveStreamsFromSubjects(tc.subs, tc.pubs)
			sortStreamsByName(got)
			sortStreamsByName(tc.want)
			if len(got) != len(tc.want) {
				t.Fatalf("len: got %d want %d (%+v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i].Name != tc.want[i].Name {
					t.Fatalf("[%d].Name: got %q want %q", i, got[i].Name, tc.want[i].Name)
				}
				if len(got[i].Subjects) != len(tc.want[i].Subjects) || got[i].Subjects[0] != tc.want[i].Subjects[0] {
					t.Fatalf("[%d].Subjects: got %v want %v", i, got[i].Subjects, tc.want[i].Subjects)
				}
			}
		})
	}
}

func sortStreamsByName(s []rawStream) {
	sort.Slice(s, func(i, j int) bool { return s[i].Name < s[j].Name })
}

func TestResolveDurableQueueGroup_Matrix(t *testing.T) {
	cases := []struct {
		name           string
		sub            rawSubscriber
		serverGroup    string
		wantDurable    string
		wantQueueGroup string
	}{
		{
			name:           "both empty → auto both",
			sub:            rawSubscriber{Name: "inv", Durable: "", QueueGroup: ""},
			wantDurable:    "inv",
			wantQueueGroup: "inv",
		},
		{
			name:           "explicit queue_group only",
			sub:            rawSubscriber{Name: "inv", Durable: "", QueueGroup: "workers"},
			wantDurable:    "inv",
			wantQueueGroup: "workers",
		},
		{
			name:           "explicit durable only",
			sub:            rawSubscriber{Name: "inv", Durable: "foo", QueueGroup: ""},
			wantDurable:    "foo",
			wantQueueGroup: "",
		},
		{
			name:           "both explicit",
			sub:            rawSubscriber{Name: "inv", Durable: "foo", QueueGroup: "bar"},
			wantDurable:    "foo",
			wantQueueGroup: "bar",
		},
		{
			name:           "ephemeral sentinel",
			sub:            rawSubscriber{Name: "inv", Durable: "ephemeral", QueueGroup: ""},
			wantDurable:    "",
			wantQueueGroup: "",
		},
		{
			name:           "ephemeral + explicit queue_group",
			sub:            rawSubscriber{Name: "inv", Durable: "ephemeral", QueueGroup: "bar"},
			wantDurable:    "",
			wantQueueGroup: "bar",
		},
		{
			name:           "auto both with server group suffix",
			sub:            rawSubscriber{Name: "inv", Durable: "", QueueGroup: ""},
			serverGroup:    "dc1",
			wantDurable:    "inv",
			wantQueueGroup: "inv-dc1",
		},
		{
			name:           "explicit queue_group NOT suffixed",
			sub:            rawSubscriber{Name: "inv", Durable: "", QueueGroup: "workers"},
			serverGroup:    "dc1",
			wantDurable:    "inv",
			wantQueueGroup: "workers",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, qg := resolveDurableQueueGroup(&tc.sub, tc.serverGroup)
			if d != tc.wantDurable {
				t.Fatalf("durable: got %q want %q", d, tc.wantDurable)
			}
			if qg != tc.wantQueueGroup {
				t.Fatalf("queue_group: got %q want %q", qg, tc.wantQueueGroup)
			}
		})
	}
}
