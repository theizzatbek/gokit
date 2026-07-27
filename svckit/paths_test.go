package svckit

import "testing"

func TestResolvePathInDir_OverridePassesThrough(t *testing.T) {
	// Explicit per-subsystem Path wins even when ConfigsDir is set —
	// operators expect their literal path to be honoured.
	got := resolvePathInDir("configs", "weird.yaml", "default.yaml", true)
	if want := "weird.yaml"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolvePathInDir_AbsoluteOverridePassesThrough(t *testing.T) {
	// Absolute override stays absolute — verifies ConfigsDir does not
	// silently prefix /etc/foo.yaml into configs//etc/foo.yaml.
	got := resolvePathInDir("configs", "/etc/foo.yaml", "default.yaml", true)
	if want := "/etc/foo.yaml"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolvePathInDir_PrefixesDefaultWhenEnabled(t *testing.T) {
	got := resolvePathInDir("configs", "", "routes.yaml", true)
	if want := "configs/routes.yaml"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolvePathInDir_NoConfigsDir_DefaultName(t *testing.T) {
	// Empty ConfigsDir preserves current CWD-relative behaviour.
	got := resolvePathInDir("", "", "routes.yaml", true)
	if want := "routes.yaml"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolvePathInDir_Disabled_NoConfigsDir(t *testing.T) {
	got := resolvePathInDir("configs", "", "routes.yaml", false)
	if got != "" {
		t.Fatalf("got %q want empty (disabled, no userPath)", got)
	}
}

func TestResolvePathInDir_PathOnly_BackwardCompat(t *testing.T) {
	// A non-empty userPath triggers the subsystem even when enabled is
	// false — preserves the original "Path-presence is the opt-in"
	// behaviour regardless of ConfigsDir.
	got := resolvePathInDir("configs", "explicit.yaml", "default.yaml", false)
	if want := "explicit.yaml"; got != want {
		t.Fatalf("got %q want %q (Path-set without Enabled must still trigger)", got, want)
	}
}
