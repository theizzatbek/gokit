package svckit

import (
	"os"
	"strings"
)

// telemetryEnvs — the variables that in v1 turned telemetry on by
// themselves (see service/env_defaults.go). The core can no longer do
// that: a mod's code only lands in the binary when main mentions it.
// Instead of auto-connecting, the core warns, so the migration doesn't
// silently swallow telemetry.
var telemetryEnvs = []struct {
	env     string
	modPkg  string // how to name it in the warning text
	modName string // the Mod.Name() the mod connects under
}{
	{"OTEL_EXPORTER_OTLP_ENDPOINT", "otelmod", "otel"},
	{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "otelmod", "otel"},
	{"SENTRY_DSN", "sentrymod", "sentry"},
}

// warnOrphanedEnv accumulates warnings about telemetry configured
// operationally but not connected in code. The logger isn't built yet
// at this point, so warnings go into options — New flushes them right
// after the logger is assembled.
//
// OTEL_SDK_DISABLED=true is the W3C-standard kill switch (v1 honours
// it in otelkit.Setup) — an operator who deliberately disabled OTel
// this way but left OTEL_EXPORTER_OTLP_ENDPOINT in the environment
// (e.g. a shared base .env) should NOT get a false "telemetry will not
// be sent" warning on every boot. The switch is OTel-specific — it
// must not silence the unrelated SENTRY_DSN warning.
func warnOrphanedEnv(o *options) {
	otelDisabled := strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true")
	connected := make(map[string]struct{}, len(o.mods))
	for _, m := range o.mods {
		connected[m.Name()] = struct{}{}
	}
	seen := make(map[string]struct{}, len(telemetryEnvs))
	for _, te := range telemetryEnvs {
		if te.modName == "otel" && otelDisabled {
			continue
		}
		if os.Getenv(te.env) == "" {
			continue
		}
		if _, ok := connected[te.modName]; ok {
			continue
		}
		if _, dup := seen[te.modPkg]; dup {
			continue // one mod — one warning
		}
		seen[te.modPkg] = struct{}{}
		o.bootWarnings = append(o.bootWarnings,
			te.env+" is set, but "+te.modPkg+" is not connected — telemetry will not be sent")
	}
}

// applyEnvDefaults — carries over the CORS branch from
// service/env_defaults.go. The Sentry and OTel branches are dropped:
// their role now belongs to warnOrphanedEnv.
func applyEnvDefaults(o *options, cfg Config) {
	if o.corsWired {
		return
	}
	raw := cfg.Service.CORSOrigins
	if raw == "" {
		return
	}
	var origins []string
	for _, part := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(part); s != "" {
			origins = append(origins, s)
		}
	}
	if len(origins) == 0 {
		return
	}
	WithCORS(origins...)(o)
}
