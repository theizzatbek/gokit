package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeTracer records every call against it and stores its own value
// in the context returned from Start so End can read it back. This
// lets the test assert each child saw its OWN context, not the shared
// outer one.
type fakeTracer struct {
	name        string
	startCalls  []string
	endCalls    []string
	endSawValue []string
}

type fakeCtxKey struct{ name string }

func (f *fakeTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	f.startCalls = append(f.startCalls, data.SQL)
	return context.WithValue(ctx, fakeCtxKey{name: f.name}, f.name+":"+data.SQL)
}

func (f *fakeTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	f.endCalls = append(f.endCalls, "end")
	if v, _ := ctx.Value(fakeCtxKey{name: f.name}).(string); v != "" {
		f.endSawValue = append(f.endSawValue, v)
	}
}

func TestMultiTracer_FansOutToAllChildren(t *testing.T) {
	a := &fakeTracer{name: "a"}
	b := &fakeTracer{name: "b"}
	m := &multiTracer{tracers: []pgx.QueryTracer{a, b}}

	ctx := m.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	m.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	if len(a.startCalls) != 1 || a.startCalls[0] != "SELECT 1" {
		t.Errorf("a.startCalls = %v, want [SELECT 1]", a.startCalls)
	}
	if len(b.startCalls) != 1 || b.startCalls[0] != "SELECT 1" {
		t.Errorf("b.startCalls = %v, want [SELECT 1]", b.startCalls)
	}
	if len(a.endCalls) != 1 || len(b.endCalls) != 1 {
		t.Errorf("end calls a=%d b=%d, want 1+1", len(a.endCalls), len(b.endCalls))
	}
}

func TestMultiTracer_EachChildSeesOwnContext(t *testing.T) {
	a := &fakeTracer{name: "a"}
	b := &fakeTracer{name: "b"}
	m := &multiTracer{tracers: []pgx.QueryTracer{a, b}}

	ctx := m.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 2"})
	m.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	if len(a.endSawValue) != 1 || a.endSawValue[0] != "a:SELECT 2" {
		t.Errorf("a saw %v, want [a:SELECT 2] — child context not propagated", a.endSawValue)
	}
	if len(b.endSawValue) != 1 || b.endSawValue[0] != "b:SELECT 2" {
		t.Errorf("b saw %v, want [b:SELECT 2] — child context not propagated", b.endSawValue)
	}
}

func TestComposeTracer_NothingWiredReturnsNil(t *testing.T) {
	if got := composeTracer(&options{}); got != nil {
		t.Errorf("composeTracer with empty opts = %v, want nil (no overhead)", got)
	}
}

func TestComposeTracer_SingleSourceReturnsBareTracer(t *testing.T) {
	// One extra tracer + nothing kit-side → return the tracer directly
	// (avoid the multiTracer fan-out overhead).
	x := &fakeTracer{name: "x"}
	got := composeTracer(&options{extraTracers: []pgx.QueryTracer{x}})
	if got != x {
		t.Errorf("composeTracer = %T, want bare fakeTracer", got)
	}
}

func TestComposeTracer_MultipleSourcesReturnsMulti(t *testing.T) {
	x := &fakeTracer{name: "x"}
	y := &fakeTracer{name: "y"}
	got := composeTracer(&options{extraTracers: []pgx.QueryTracer{x, y}})
	if _, ok := got.(*multiTracer); !ok {
		t.Errorf("composeTracer = %T, want *multiTracer", got)
	}
}
