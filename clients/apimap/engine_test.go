package apimap

import (
	"errors"
	"testing"

	xerrs "github.com/theizzatbek/fibermap/errs"
)

type stubReq struct{ N int }
type stubResp struct{ S string }

func TestEngine_LoadBytes_Minimal(t *testing.T) {
	e := New()
	yaml := []byte(`clients:
  - name: c1
    base_url: https://example.com
    endpoints:
      - {name: a, method: GET, path: /a}
`)
	if err := e.LoadBytes(yaml); err != nil {
		t.Fatal(err)
	}
	if len(e.clients) != 1 || e.clients[0].Name != "c1" {
		t.Errorf("clients = %+v", e.clients)
	}
}

func TestEngine_LoadFile_Minimal(t *testing.T) {
	e := New()
	if err := e.LoadFile("testdata/minimal.yaml"); err != nil {
		t.Fatal(err)
	}
	if len(e.clients) != 1 {
		t.Errorf("clients = %d, want 1", len(e.clients))
	}
}

func TestEngine_LoadMulti_AppendsClients(t *testing.T) {
	e := New()
	if err := e.LoadFile("testdata/minimal.yaml"); err != nil {
		t.Fatal(err)
	}
	if err := e.LoadFile("testdata/multi_client.yaml"); err != nil {
		t.Fatal(err)
	}
	if len(e.clients) != 3 {
		t.Errorf("clients = %d, want 3 (1 + 2)", len(e.clients))
	}
}

func TestEngine_RegisterRequest_StoresType(t *testing.T) {
	e := New()
	RegisterRequest[stubReq](e, "c1.create")
	if _, ok := e.reqTypes["c1.create"]; !ok {
		t.Error("request type not stored")
	}
}

func TestEngine_RegisterResponse_StoresType(t *testing.T) {
	e := New()
	RegisterResponse[stubResp](e, "c1.fetch")
	if _, ok := e.respTypes["c1.fetch"]; !ok {
		t.Error("response type not stored")
	}
}

func TestEngine_DuplicateRegisterPanics(t *testing.T) {
	e := New()
	RegisterResponse[stubResp](e, "c1.fetch")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("recovered non-error %v", r)
		}
		var xe *xerrs.Error
		if !errors.As(err, &xe) || xe.Code != CodeDuplicateEndpoint {
			t.Errorf("err = %v, want code %s", err, CodeDuplicateEndpoint)
		}
	}()
	RegisterResponse[stubResp](e, "c1.fetch")
}

func TestEngine_RegisterAfterBuiltPanics(t *testing.T) {
	e := New()
	e.built = true // simulate post-build state
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on register after Build")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("recovered non-error %v", r)
		}
		var xe *xerrs.Error
		if !errors.As(err, &xe) || xe.Code != CodeAlreadyBuilt {
			t.Errorf("err = %v, want code %s", err, CodeAlreadyBuilt)
		}
	}()
	RegisterResponse[stubResp](e, "c1.fetch")
}
