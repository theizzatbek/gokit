package service

import "testing"

func TestResolvePath_OverrideWins(t *testing.T) {
	got := resolvePath("custom.yaml", "default.yaml", true)
	if want := "custom.yaml"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolvePath_EnabledOnly_UsesDefault(t *testing.T) {
	got := resolvePath("", "default.yaml", true)
	if want := "default.yaml"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolvePath_PathOnly_BackwardCompat(t *testing.T) {
	got := resolvePath("explicit.yaml", "default.yaml", false)
	if want := "explicit.yaml"; got != want {
		t.Fatalf("got %q want %q (Path-set without Enabled must still trigger)", got, want)
	}
}

func TestResolvePath_NeitherSet_ReturnsEmpty(t *testing.T) {
	got := resolvePath("", "default.yaml", false)
	if got != "" {
		t.Fatalf("got %q want empty", got)
	}
}
