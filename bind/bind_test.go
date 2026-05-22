package bind_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/theizzatbek/fibermap/bind"
)

// fakeCtx is a BodyParser that returns canned JSON bytes.
type fakeCtx struct {
	body []byte
	err  error
}

func (f fakeCtx) BodyParser(out any) error {
	if f.err != nil {
		return f.err
	}
	return json.Unmarshal(f.body, out)
}

// fakeValidator is a Validator that returns a canned error.
type fakeValidator struct{ err error }

func (f fakeValidator) Struct(any) error { return f.err }

type createReq struct {
	Title string `json:"title"`
}

func TestBody_Happy(t *testing.T) {
	c := fakeCtx{body: []byte(`{"title":"buy milk"}`)}
	req, err := bind.Body[createReq](c, fakeValidator{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if req.Title != "buy milk" {
		t.Errorf("Title = %q, want buy milk", req.Title)
	}
}

func TestBody_ParseError(t *testing.T) {
	c := fakeCtx{err: errors.New("bad json")}
	_, err := bind.Body[createReq](c, fakeValidator{})
	if !errors.Is(err, bind.ErrParseBody) {
		t.Errorf("err = %v, want wrap of ErrParseBody", err)
	}
}

func TestBody_ValidationError(t *testing.T) {
	c := fakeCtx{body: []byte(`{"title":""}`)}
	v := fakeValidator{err: errors.New("Title is required")}
	_, err := bind.Body[createReq](c, v)
	if !errors.Is(err, bind.ErrValidateBody) {
		t.Errorf("err = %v, want wrap of ErrValidateBody", err)
	}
}

func TestBody_NilValidator_Skips(t *testing.T) {
	// Passing nil validator is allowed — useful when the body type is
	// trivially safe (e.g. struct of one bool) and you don't want to
	// wire a validator.
	c := fakeCtx{body: []byte(`{"title":"x"}`)}
	req, err := bind.Body[createReq](c, nil)
	if err != nil {
		t.Fatalf("err = %v, want nil with nil validator", err)
	}
	if req.Title != "x" {
		t.Errorf("Title = %q", req.Title)
	}
}
