package natsmap

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

type wrongType struct{ X int }

func TestPublish_UnknownPublisher_Errors(t *testing.T) {
	rt := &Runtime{publishers: map[string]publishShim{}}
	err := Publish[tHandlerPayload](context.Background(), rt, "no-such", tHandlerPayload{ID: "x"})
	if err == nil || !strings.Contains(err.Error(), CodeUnknownPublisher) {
		t.Fatalf("want CodeUnknownPublisher, got %v", err)
	}
}

func TestPublish_TypeMismatch_Errors(t *testing.T) {
	shim := publishShim{
		subject:     "orders.created",
		payloadType: reflect.TypeOf(tHandlerPayload{}),
		publish: func(ctx context.Context, payload any, hdrs map[string][]string) error {
			t.Fatalf("publish shim should not be called on type mismatch")
			return nil
		},
	}
	rt := &Runtime{publishers: map[string]publishShim{"orders.created": shim}}
	err := Publish[wrongType](context.Background(), rt, "orders.created", wrongType{X: 1})
	if err == nil || !strings.Contains(err.Error(), CodePublisherTypeMismatch) {
		t.Fatalf("want CodePublisherTypeMismatch, got %v", err)
	}
}

func TestDrain_IsIdempotent(t *testing.T) {
	rt := &Runtime{}
	if err := rt.Drain(); err != nil {
		t.Fatalf("first drain: %v", err)
	}
	if err := rt.Drain(); err != nil {
		t.Fatalf("second drain: %v", err)
	}
}

func TestMergeHeaders_PerCallWinsOnCollision(t *testing.T) {
	staticHdrs := map[string][]string{"X-Source": {"static"}, "X-Other": {"keep"}}
	callHdrs := map[string][]string{"X-Source": {"call"}}
	merged := mergeHeaders(staticHdrs, callHdrs)
	if got, want := merged["X-Source"][0], "call"; got != want {
		t.Fatalf("X-Source: got %q want %q (per-call should win)", got, want)
	}
	if got, want := merged["X-Other"][0], "keep"; got != want {
		t.Fatalf("X-Other: got %q want %q (static should be preserved)", got, want)
	}
}

func TestExpandHeaders_PreservesEmptyAsNil(t *testing.T) {
	if got := expandHeaders(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
	if got := expandHeaders(map[string]string{}); got != nil {
		t.Fatalf("expected nil for empty map, got %v", got)
	}
	in := map[string]string{"X-Source": "svc"}
	out := expandHeaders(in)
	if got, want := out["X-Source"][0], "svc"; got != want {
		t.Fatalf("X-Source: got %q want %q", got, want)
	}
}

func TestPublisherNames_Sorted(t *testing.T) {
	rt := &Runtime{publishers: map[string]publishShim{
		"b": {}, "a": {}, "c": {},
	}}
	got := rt.PublisherNames()
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("PublisherNames not sorted: %v", got)
	}
}
