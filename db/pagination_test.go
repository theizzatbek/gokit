package db

import (
	"context"
	"errors"
	"testing"

	"github.com/theizzatbek/gokit/errs"
)

func TestEncodeDecodeCursor_RoundTrip(t *testing.T) {
	type k struct {
		ID  string
		Ord int
	}
	in := k{ID: "user-42", Ord: 100}
	cur, err := EncodeCursor(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if cur == "" {
		t.Fatal("encoded cursor is empty")
	}
	out, err := DecodeCursor[k](cur)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out != in {
		t.Errorf("round trip: %+v, want %+v", out, in)
	}
}

func TestDecodeCursor_EmptyReturnsZero(t *testing.T) {
	got, err := DecodeCursor[string]("")
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDecodeCursor_MalformedFailsValidation(t *testing.T) {
	_, err := DecodeCursor[string]("!!! not base64 !!!")
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		t.Errorf("err = %v, want KindValidation", err)
	}
}

func TestPaginate_NilQuerier(t *testing.T) {
	_, err := Paginate[string, string](context.Background(), nil, "SELECT 1", 10, nil)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != "db_nil_querier" {
		t.Errorf("err = %v, want db_nil_querier", err)
	}
}

func TestPaginate_InvalidLimit(t *testing.T) {
	q := &stubQuerier{}
	_, err := Paginate[string, string](context.Background(), q, "SELECT 1", 0, nil)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != "db_invalid_limit" {
		t.Errorf("err = %v, want db_invalid_limit", err)
	}
}
