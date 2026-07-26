package svckit

import (
	"strings"
	"testing"
)

func TestWarnOrphanedEnv_WarnsWhenModMissing(t *testing.T) {
	t.Setenv("SENTRY_DSN", "https://example@sentry.io/1")
	o := &options{}

	warnOrphanedEnv(o)

	if len(o.bootWarnings) != 1 {
		t.Fatalf("want 1 warning, got %v", o.bootWarnings)
	}
	if !strings.Contains(o.bootWarnings[0], "sentrymod") {
		t.Errorf("warning must name the mod: %q", o.bootWarnings[0])
	}
}

func TestWarnOrphanedEnv_SilentWhenModConnected(t *testing.T) {
	t.Setenv("SENTRY_DSN", "https://example@sentry.io/1")
	o := &options{mods: []Mod{nameOnlyMod{name: "sentry"}}}

	warnOrphanedEnv(o)

	if len(o.bootWarnings) != 0 {
		t.Errorf("mod is connected — nothing to warn about: %v", o.bootWarnings)
	}
}

func TestWarnOrphanedEnv_SilentWhenEnvUnset(t *testing.T) {
	t.Setenv("SENTRY_DSN", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	o := &options{}

	warnOrphanedEnv(o)

	if len(o.bootWarnings) != 0 {
		t.Errorf("env is empty — nothing to warn about: %v", o.bootWarnings)
	}
}
