package fibermap

import (
	"reflect"
	"testing"
)

func TestResolveChain_FlatOrder(t *testing.T) {
	sets := map[string][]string{}
	got := resolveChain(sets, [][]string{{"logger"}, {"auth"}}, []string{"audit"}, false)
	want := []string{"logger", "auth", "audit"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveChain_Dedup(t *testing.T) {
	got := resolveChain(nil, [][]string{{"auth", "logger"}, {"auth"}}, []string{"logger", "audit"}, false)
	want := []string{"auth", "logger", "audit"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveChain_ExpandSet(t *testing.T) {
	sets := map[string][]string{
		"protected": {"logger", "auth"},
	}
	// One ancestor-group's chain references the set name. resolveChain treats
	// any name that exists in `sets` as a set to expand recursively.
	got := resolveChain(sets, [][]string{{"protected", "authorized"}}, nil, false)
	want := []string{"logger", "auth", "authorized"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveChain_NestedSets(t *testing.T) {
	sets := map[string][]string{
		"base":      {"logger"},
		"protected": {"base", "auth"},
	}
	got := resolveChain(sets, [][]string{{"protected"}}, nil, false)
	want := []string{"logger", "auth"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveChain_RoleGuardAppended(t *testing.T) {
	got := resolveChain(nil, [][]string{{"auth"}}, nil, true)
	want := []string{"auth", "__role_guard__"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
