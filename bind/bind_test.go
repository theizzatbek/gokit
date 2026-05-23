package bind_test

import (
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strconv"
	"testing"

	"github.com/theizzatbek/fibermap/bind"
)

// fakeCtx is a BodyParser/QueryParser/ParamsParser/ReqHeaderParser
// that returns canned data.
type fakeCtx struct {
	body []byte
	err  error

	query   url.Values
	qerr    error
	params  map[string]string
	perr    error
	headers map[string]string
	herr    error
}

func (f fakeCtx) BodyParser(out any) error {
	if f.err != nil {
		return f.err
	}
	return json.Unmarshal(f.body, out)
}

// QueryParser decodes url.Values into struct fields by `query:` tag.
// Supports string and int; sufficient for tests.
func (f fakeCtx) QueryParser(out any) error {
	if f.qerr != nil {
		return f.qerr
	}
	return decodeKV(f.query, out, "query")
}

func (f fakeCtx) ParamsParser(out any) error {
	if f.perr != nil {
		return f.perr
	}
	values := url.Values{}
	for k, v := range f.params {
		values.Set(k, v)
	}
	return decodeKV(values, out, "params")
}

func decodeKV(values url.Values, out any, tag string) error {
	rv := reflect.ValueOf(out).Elem()
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		key := rt.Field(i).Tag.Get(tag)
		if key == "" {
			continue
		}
		raw := values.Get(key)
		if raw == "" {
			continue
		}
		f := rv.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(raw)
		case reflect.Int, reflect.Int64:
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return err
			}
			f.SetInt(n)
		}
	}
	return nil
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
	c := fakeCtx{body: []byte(`{"title":"x"}`)}
	req, err := bind.Body[createReq](c, nil)
	if err != nil {
		t.Fatalf("err = %v, want nil with nil validator", err)
	}
	if req.Title != "x" {
		t.Errorf("Title = %q", req.Title)
	}
}

type listQuery struct {
	Limit  int    `query:"limit"`
	Cursor string `query:"cursor"`
}

func TestQuery_Happy(t *testing.T) {
	c := fakeCtx{query: url.Values{"limit": {"50"}, "cursor": {"abc"}}}
	q, err := bind.Query[listQuery](c, fakeValidator{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if q.Limit != 50 || q.Cursor != "abc" {
		t.Errorf("got %+v, want {50 abc}", q)
	}
}

func TestQuery_ParseError(t *testing.T) {
	c := fakeCtx{qerr: errors.New("bad query")}
	_, err := bind.Query[listQuery](c, fakeValidator{})
	if !errors.Is(err, bind.ErrParseQuery) {
		t.Errorf("err = %v, want wrap of ErrParseQuery", err)
	}
}

func TestQuery_ValidationError(t *testing.T) {
	c := fakeCtx{query: url.Values{"limit": {"0"}}}
	v := fakeValidator{err: errors.New("limit must be > 0")}
	_, err := bind.Query[listQuery](c, v)
	if !errors.Is(err, bind.ErrValidateQuery) {
		t.Errorf("err = %v, want wrap of ErrValidateQuery", err)
	}
}

func TestQuery_NilValidator_Skips(t *testing.T) {
	c := fakeCtx{query: url.Values{"limit": {"5"}}}
	q, err := bind.Query[listQuery](c, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if q.Limit != 5 {
		t.Errorf("Limit = %d, want 5", q.Limit)
	}
}

type idParams struct {
	ID string `params:"id"`
}

func TestParams_Happy(t *testing.T) {
	c := fakeCtx{params: map[string]string{"id": "42"}}
	p, err := bind.Params[idParams](c, fakeValidator{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if p.ID != "42" {
		t.Errorf("ID = %q, want 42", p.ID)
	}
}

func TestParams_ParseError(t *testing.T) {
	c := fakeCtx{perr: errors.New("bad params")}
	_, err := bind.Params[idParams](c, fakeValidator{})
	if !errors.Is(err, bind.ErrParseParams) {
		t.Errorf("err = %v, want wrap of ErrParseParams", err)
	}
}

func TestParams_ValidationError(t *testing.T) {
	c := fakeCtx{params: map[string]string{"id": ""}}
	v := fakeValidator{err: errors.New("id is required")}
	_, err := bind.Params[idParams](c, v)
	if !errors.Is(err, bind.ErrValidateParams) {
		t.Errorf("err = %v, want wrap of ErrValidateParams", err)
	}
}

func TestParams_NilValidator_Skips(t *testing.T) {
	c := fakeCtx{params: map[string]string{"id": "abc"}}
	p, err := bind.Params[idParams](c, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if p.ID != "abc" {
		t.Errorf("ID = %q, want abc", p.ID)
	}
}

// fakeCtx already implements BodyParser/QueryParser/ParamsParser via
// generic decodeKV. Add a ReqHeaderParser by reusing the same path.
func (f fakeCtx) ReqHeaderParser(out any) error {
	if f.herr != nil {
		return f.herr
	}
	values := url.Values{}
	for k, v := range f.headers {
		values.Set(k, v)
	}
	return decodeKV(values, out, "reqHeader")
}

type authHeader struct {
	Authorization string `reqHeader:"Authorization"`
	TraceID       string `reqHeader:"X-Trace-Id"`
}

func TestHeader_Happy(t *testing.T) {
	c := fakeCtx{headers: map[string]string{
		"Authorization": "Bearer abc",
		"X-Trace-Id":    "trace-123",
	}}
	h, err := bind.Header[authHeader](c, fakeValidator{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if h.Authorization != "Bearer abc" || h.TraceID != "trace-123" {
		t.Errorf("got %+v", h)
	}
}

func TestHeader_ParseError(t *testing.T) {
	c := fakeCtx{herr: errors.New("bad header")}
	_, err := bind.Header[authHeader](c, fakeValidator{})
	if !errors.Is(err, bind.ErrParseHeader) {
		t.Errorf("err = %v, want wrap of ErrParseHeader", err)
	}
}

func TestHeader_ValidationError(t *testing.T) {
	c := fakeCtx{headers: map[string]string{"Authorization": ""}}
	v := fakeValidator{err: errors.New("Authorization required")}
	_, err := bind.Header[authHeader](c, v)
	if !errors.Is(err, bind.ErrValidateHeader) {
		t.Errorf("err = %v, want wrap of ErrValidateHeader", err)
	}
}

func TestHeader_NilValidator_Skips(t *testing.T) {
	c := fakeCtx{headers: map[string]string{"Authorization": "anything"}}
	h, err := bind.Header[authHeader](c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h.Authorization != "anything" {
		t.Errorf("Authorization = %q", h.Authorization)
	}
}
