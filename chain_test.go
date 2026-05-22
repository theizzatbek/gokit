package fibermap

import (
	"reflect"
	"testing"
)

func ref(name string, args ...string) mwRef {
	if len(args) == 0 {
		return mwRef{Name: name}
	}
	return mwRef{Name: name, Args: args}
}

func names(rs []mwRef) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

func TestResolveChain_FlatOrder(t *testing.T) {
	got := resolveChain(nil, [][]mwRef{{ref("logger")}, {ref("auth")}}, []mwRef{ref("audit")})
	if want := []string{"logger", "auth", "audit"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("got %v, want %v", names(got), want)
	}
}

func TestResolveChain_Dedup(t *testing.T) {
	got := resolveChain(nil,
		[][]mwRef{{ref("auth"), ref("logger")}, {ref("auth")}},
		[]mwRef{ref("logger"), ref("audit")})
	if want := []string{"auth", "logger", "audit"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("got %v, want %v", names(got), want)
	}
}

func TestResolveChain_DedupWithArgs(t *testing.T) {
	// Same factory name, different args → kept as separate entries.
	got := resolveChain(nil, nil, []mwRef{
		ref("require_role", "director"),
		ref("require_role", "admin"),
		ref("require_role", "director"), // duplicate of first
	})
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	if got[0].Args[0] != "director" || got[1].Args[0] != "admin" {
		t.Errorf("got %+v", got)
	}
}

func TestResolveChain_ExpandSet(t *testing.T) {
	sets := map[string][]mwRef{
		"protected": {ref("logger"), ref("auth")},
	}
	got := resolveChain(sets, [][]mwRef{{ref("protected"), ref("authorized")}}, nil)
	if want := []string{"logger", "auth", "authorized"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("got %v, want %v", names(got), want)
	}
}

func TestResolveChain_NestedSets(t *testing.T) {
	sets := map[string][]mwRef{
		"base":      {ref("logger")},
		"protected": {ref("base"), ref("auth")},
	}
	got := resolveChain(sets, [][]mwRef{{ref("protected")}}, nil)
	if want := []string{"logger", "auth"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("got %v, want %v", names(got), want)
	}
}

func TestResolveChain_FactoryInsideSet(t *testing.T) {
	sets := map[string][]mwRef{
		"director_only": {ref("logger"), ref("require_role", "director")},
	}
	got := resolveChain(sets, [][]mwRef{{ref("director_only")}}, nil)
	if len(got) != 2 || got[1].Name != "require_role" || got[1].Args[0] != "director" {
		t.Errorf("got %+v", got)
	}
}