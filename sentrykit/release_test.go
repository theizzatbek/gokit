package sentrykit

import (
	"runtime/debug"
	"testing"
)

func TestAutoRelease_EnvWins(t *testing.T) {
	t.Setenv("SENTRY_RELEASE", "svc@deadbeef")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=2.0.0")
	if got := AutoRelease(); got != "svc@deadbeef" {
		t.Errorf("AutoRelease = %q, want svc@deadbeef (env wins over OTel attr)", got)
	}
}

func TestAutoRelease_OtelResourceAttr(t *testing.T) {
	t.Setenv("SENTRY_RELEASE", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=foo,service.version=1.2.3,region=us-east-1")
	if got := AutoRelease(); got != "1.2.3" {
		t.Errorf("AutoRelease = %q, want 1.2.3", got)
	}
}

func TestAutoRelease_OtelResourceAttr_HandlesQuotedAndEncodedValues(t *testing.T) {
	t.Setenv("SENTRY_RELEASE", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", `service.version="v1.0.0-rc.1%20build42"`)
	if got := AutoRelease(); got != "v1.0.0-rc.1 build42" {
		t.Errorf("AutoRelease = %q, want 'v1.0.0-rc.1 build42'", got)
	}
}

func TestAutoReleaseFromBuildInfo_MainVersion(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "v2.1.0"}}
	if got := autoReleaseFromBuildInfo(info); got != "v2.1.0" {
		t.Errorf("got %q, want v2.1.0", got)
	}
}

func TestAutoReleaseFromBuildInfo_DevelFallsToVCS(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.modified", Value: "false"},
			{Key: "vcs.revision", Value: "abcdef1234567890abcdef1234567890abcdef12"},
		},
	}
	if got := autoReleaseFromBuildInfo(info); got != "abcdef123456" {
		t.Errorf("got %q, want abcdef123456 (12-char vcs.revision truncation)", got)
	}
}

func TestAutoReleaseFromBuildInfo_DevelShortVCS(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
		},
	}
	if got := autoReleaseFromBuildInfo(info); got != "abc123" {
		t.Errorf("got %q, want abc123 (short revision passes through)", got)
	}
}

func TestAutoReleaseFromBuildInfo_Empty(t *testing.T) {
	t.Setenv("SENTRY_RELEASE", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	if got := autoReleaseFromBuildInfo(&debug.BuildInfo{}); got != "" {
		t.Errorf("empty build info: got %q, want \"\"", got)
	}
	if got := autoReleaseFromBuildInfo(nil); got != "" {
		t.Errorf("nil build info: got %q, want \"\"", got)
	}
}
