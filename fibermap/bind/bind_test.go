package bind_test

import (
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strconv"
	"testing"

	"github.com/theizzatbek/gokit/fibermap/bind"
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

// typedValidationErr is a custom error type returned by fakeValidator
// in the *_PreservesInnerType tests below. Using a kit-local type lets
// the tests stay self-contained while exercising the same `errors.As`
// path that go-playground/validator/v10's ValidationErrors needs in
// real callers (errsval.FromValidator depends on it).
type typedValidationErr struct{ Field string }

func (e *typedValidationErr) Error() string { return "validation failed on " + e.Field }

// errParseSentinel is a typed error returned by the fakeCtx parsers in
// the *_ParseErrorPreservesInnerType tests below. Same rationale as
// typedValidationErr — exercising errors.As through the bind wrapper.
type errParseSentinel struct{ Reason string }

func (e *errParseSentinel) Error() string { return "parse failed: " + e.Reason }

// TestBody_ValidationErrorPreservesInnerType regression-guards the
// fix for LicenseKit P0-1: the bind wrap must keep the inner error
// type addressable via errors.As, not just stringified into a sentinel
// wrap. Without errors.Join (or fmt.Errorf("%w: %w", ...)) the inner
// type is lost and errsval.FromValidator can't recover Details[].
func TestBody_ValidationErrorPreservesInnerType(t *testing.T) {
	inner := &typedValidationErr{Field: "title"}
	c := fakeCtx{body: []byte(`{"title":""}`)}
	_, err := bind.Body[createReq](c, fakeValidator{err: inner})
	if !errors.Is(err, bind.ErrValidateBody) {
		t.Fatalf("errors.Is(err, ErrValidateBody) = false; sentinel chain broken")
	}
	var got *typedValidationErr
	if !errors.As(err, &got) {
		t.Fatalf("errors.As(err, &typedValidationErr) = false; inner error type lost")
	}
	if got.Field != "title" {
		t.Errorf("got.Field = %q, want title", got.Field)
	}
}

func TestBody_ParseErrorPreservesInnerType(t *testing.T) {
	inner := &errParseSentinel{Reason: "bad json"}
	c := fakeCtx{err: inner}
	_, err := bind.Body[createReq](c, fakeValidator{})
	if !errors.Is(err, bind.ErrParseBody) {
		t.Fatalf("errors.Is(err, ErrParseBody) = false; sentinel chain broken")
	}
	var got *errParseSentinel
	if !errors.As(err, &got) {
		t.Fatalf("errors.As(err, &errParseSentinel) = false; inner error type lost")
	}
}

func TestQuery_ValidationErrorPreservesInnerType(t *testing.T) {
	inner := &typedValidationErr{Field: "limit"}
	c := fakeCtx{query: url.Values{"limit": {"0"}}}
	_, err := bind.Query[listQuery](c, fakeValidator{err: inner})
	if !errors.Is(err, bind.ErrValidateQuery) {
		t.Fatalf("errors.Is(err, ErrValidateQuery) = false")
	}
	var got *typedValidationErr
	if !errors.As(err, &got) {
		t.Fatalf("errors.As(err, &typedValidationErr) = false")
	}
	if got.Field != "limit" {
		t.Errorf("got.Field = %q, want limit", got.Field)
	}
}

func TestParams_ValidationErrorPreservesInnerType(t *testing.T) {
	inner := &typedValidationErr{Field: "id"}
	c := fakeCtx{params: map[string]string{"id": ""}}
	_, err := bind.Params[idParams](c, fakeValidator{err: inner})
	if !errors.Is(err, bind.ErrValidateParams) {
		t.Fatalf("errors.Is(err, ErrValidateParams) = false")
	}
	var got *typedValidationErr
	if !errors.As(err, &got) {
		t.Fatalf("errors.As(err, &typedValidationErr) = false")
	}
	if got.Field != "id" {
		t.Errorf("got.Field = %q, want id", got.Field)
	}
}

func TestHeader_ValidationErrorPreservesInnerType(t *testing.T) {
	inner := &typedValidationErr{Field: "Authorization"}
	c := fakeCtx{headers: map[string]string{"Authorization": ""}}
	_, err := bind.Header[authHeader](c, fakeValidator{err: inner})
	if !errors.Is(err, bind.ErrValidateHeader) {
		t.Fatalf("errors.Is(err, ErrValidateHeader) = false")
	}
	var got *typedValidationErr
	if !errors.As(err, &got) {
		t.Fatalf("errors.As(err, &typedValidationErr) = false")
	}
	if got.Field != "Authorization" {
		t.Errorf("got.Field = %q, want Authorization", got.Field)
	}
}
