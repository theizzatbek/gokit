package natsclient

import (
	"reflect"
	"testing"
)

type sample struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestJSONCodec_RoundTrip(t *testing.T) {
	c := JSONCodec{}
	in := sample{ID: "u-1", Name: "alice"}
	b, err := c.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out sample
	if err := c.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip lost data: in=%+v out=%+v", in, out)
	}
}

func TestJSONCodec_ContentType(t *testing.T) {
	if got := (JSONCodec{}).ContentType(); got != "application/json" {
		t.Fatalf("ContentType = %q, want application/json", got)
	}
}

func TestDefaultCodec_ReturnsJSON(t *testing.T) {
	c := DefaultCodec()
	if _, ok := c.(JSONCodec); !ok {
		t.Fatalf("DefaultCodec returned %T, want JSONCodec", c)
	}
}
