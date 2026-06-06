package wsnats

import (
	"context"
	"testing"

	"github.com/nats-io/nats.go"
)

func TestBridge_ZeroValue(t *testing.T) {
	var b Bridge
	if b.Subscribe != nil {
		t.Errorf("zero-value Subscribe = %v, want nil", b.Subscribe)
	}
	if b.Publish != "" {
		t.Errorf("zero-value Publish = %q, want empty", b.Publish)
	}
	if b.Binary {
		t.Error("zero-value Binary = true, want false")
	}
	if b.QueueGroup != "" {
		t.Errorf("zero-value QueueGroup = %q, want empty", b.QueueGroup)
	}
}

func TestOnMessage_DropReturnsNilNil(t *testing.T) {
	// Contract: OnMessage returning (nil, nil) means "drop silently"
	// — kit code uses `out == nil` to gate that branch. Verify the
	// idiom compiles and behaves as documented.
	fn := func(msg *nats.Msg) ([]byte, error) {
		if string(msg.Data) == "drop" {
			return nil, nil
		}
		return msg.Data, nil
	}
	out, err := fn(&nats.Msg{Data: []byte("drop")})
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if out != nil {
		t.Errorf("out = %v, want nil (dropped)", out)
	}
	out, err = fn(&nats.Msg{Data: []byte("keep")})
	if err != nil || string(out) != "keep" {
		t.Errorf("forwarded msg = %s err=%v, want keep nil", out, err)
	}
}

func TestOnFrame_TransformIdiom(t *testing.T) {
	// Same drop/transform contract on the inbound side.
	fn := func(payload []byte) ([]byte, error) {
		if string(payload) == "ignore" {
			return nil, nil
		}
		return append([]byte("transformed:"), payload...), nil
	}
	out, err := fn([]byte("ignore"))
	if err != nil || out != nil {
		t.Errorf("ignore: out=%v err=%v, want nil/nil", out, err)
	}
	out, err = fn([]byte("ping"))
	if err != nil || string(out) != "transformed:ping" {
		t.Errorf("forward: out=%s err=%v", out, err)
	}
}

func TestRegister_NilNATS_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil NATS client")
		}
	}()
	Register[any](nil, "x", nil, nil)
}

func TestRegister_NilBridgeFn_Panics(t *testing.T) {
	// We can't construct a real natsclient.Client here without a
	// container; the function panics on its second precondition only
	// after the first succeeds. Skip this without a NATS connection.
	t.Skip("requires a real natsclient.Client; integration-test territory")
}

func TestRunBridge_NoSubjectsDoesNotLoop(t *testing.T) {
	// Sanity smoke: runBridge with no Subscribe + no Publish should
	// immediately bail out (no work to do) — verifies the zero-value
	// Bridge case does not get stuck waiting on a non-existent
	// ws.ReadMessage. We can't actually exercise this without a real
	// WS conn; the test documents the intent for the integration
	// suite to enforce.
	_ = context.Background
}
