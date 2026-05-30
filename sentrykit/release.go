package sentrykit

import (
	"os"
	"runtime/debug"
	"strings"
)

// AutoRelease returns the release tag derived from (in priority
// order):
//
//  1. SENTRY_RELEASE environment variable.
//  2. OTEL_RESOURCE_ATTRIBUTES `service.version=X` if present.
//  3. runtime/debug.ReadBuildInfo().Main.Version when it is not
//     "(devel)" — typically a semver picked from a go install
//     against a tagged commit.
//  4. The "vcs.revision" build setting (truncated to 12 chars to
//     match the Sentry short-SHA release convention).
//
// Returns "" when none of the above are available — typical for
// `go run` against an untagged commit without -trimpath. The caller
// can decide whether "" is a usable release tag (sentry-go accepts
// empty and ships events without a release attribution).
//
// service.setupSentry calls AutoRelease at startup and prepends the
// result as a sentrykit.WithRelease option. Any explicit
// sentrykit.WithRelease passed by the caller overrides because the
// functional-options pipeline applies later writes last.
func AutoRelease() string {
	if v := strings.TrimSpace(os.Getenv("SENTRY_RELEASE")); v != "" {
		return v
	}
	if v := otelResourceServiceVersion(os.Getenv("OTEL_RESOURCE_ATTRIBUTES")); v != "" {
		return v
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return autoReleaseFromBuildInfo(info)
}

// autoReleaseFromBuildInfo is the testable helper that takes a
// build info struct as input (instead of reading it from the
// runtime). Tests use it to assert vcs.revision precedence without
// rebuilding the binary.
func autoReleaseFromBuildInfo(info *debug.BuildInfo) string {
	if info == nil {
		return ""
	}
	if v := strings.TrimSpace(info.Main.Version); v != "" && v != "(devel)" {
		return v
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			rev := strings.TrimSpace(s.Value)
			if rev == "" {
				return ""
			}
			if len(rev) > 12 {
				return rev[:12]
			}
			return rev
		}
	}
	return ""
}

// otelResourceServiceVersion extracts the value of the
// `service.version` attribute from an OTEL_RESOURCE_ATTRIBUTES
// string. Format per the OTel spec is a comma-separated list of
// key=value pairs, with values optionally percent-encoded. We do a
// minimal decode for the percent-encoding cases the spec calls out
// (whitespace, commas, '=') because they're the realistic ones in
// build pipelines; uncommon escapes pass through.
//
// Returns "" when the attribute is missing or its value is empty.
func otelResourceServiceVersion(attrs string) string {
	if attrs == "" {
		return ""
	}
	for _, pair := range strings.Split(attrs, ",") {
		eq := strings.IndexByte(pair, '=')
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(pair[:eq])
		if k != "service.version" {
			continue
		}
		v := strings.TrimSpace(pair[eq+1:])
		v = strings.Trim(v, `"`)
		// Minimal percent-decode for the three characters most
		// likely to appear in build pipelines.
		v = strings.NewReplacer("%20", " ", "%2C", ",", "%3D", "=").Replace(v)
		return v
	}
	return ""
}
