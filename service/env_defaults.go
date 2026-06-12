package service

import "os"

// applyEnvDefaults wires Sentry and OTel automatically when their
// respective environment variables are present AND the caller did
// not already opt-in programmatically via [WithSentry] / [WithOtel].
// Runs once, after the caller-options loop in [New], before
// setupOtel / setupSentry consume the option fields.
//
// Sentry trigger:
//
//	SENTRY_DSN=<dsn>  →  sets options.sentryDSN to the env value.
//	                     Caller's WithSentry wins (this branch only
//	                     fires when sentryDSN is still empty).
//
// OTel triggers, in order of precedence on each fresh evaluation:
//
//	OTEL_SDK_DISABLED=true     → kill switch, skip OTel entirely
//	                             regardless of endpoint env. Matches
//	                             the W3C-standard opt-out.
//	OTEL_EXPORTER_OTLP_ENDPOINT or
//	OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
//	                           → presence triggers auto-enable.
//	                             Either env alone is enough.
//
// When auto-enable triggers, the OTel service.name is resolved in
// this order:
//
//  1. OTEL_SERVICE_NAME — W3C-standard env, wins over any kit
//     config-derived fallback. Operators who set both expect this
//     to dominate.
//  2. cfg.Service.ServerGroup — the kit's notion of "fleet identity";
//     better service.name than the per-instance hostname.
//  3. cfg.Service.NodeName — last resort. NodeName defaults to the
//     OS hostname so this lands somewhere meaningful, but a busy
//     APM dashboard will see one row per box.
//  4. empty string — no auto-enable. Caller must call WithOtel
//     explicitly to supply a name.
//
// Caller-supplied WithOtel always wins: this helper only sets the
// otelServiceName slot when it is still empty.
func applyEnvDefaults(o *options, cfg Config) {
	if o.sentryDSN == "" {
		if dsn := os.Getenv("SENTRY_DSN"); dsn != "" {
			o.sentryDSN = dsn
		}
	}

	if o.otelServiceName == "" && !otelDisabledByEnv() && otelEndpointInEnv() {
		if name := pickOtelServiceNameFromEnv(cfg); name != "" {
			o.otelServiceName = name
		}
	}
}

// otelDisabledByEnv reports whether OTEL_SDK_DISABLED=true is set —
// the W3C-standard kill switch. The kit honours it for the
// auto-enable path only; an explicit WithOtel() still wins because
// applyEnvDefaults defers when otelServiceName is non-empty.
func otelDisabledByEnv() bool {
	return os.Getenv("OTEL_SDK_DISABLED") == "true"
}

// otelEndpointInEnv reports whether either of the two standard
// OTel exporter endpoint envs is set. Either is sufficient — the
// kit's otelkit.Setup honours the same envs internally, so just
// checking presence is the right gate.
func otelEndpointInEnv() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
}

// pickOtelServiceNameFromEnv resolves the OTel service.name for the
// auto-enable path. See [applyEnvDefaults] for the precedence rule
// + rationale.
func pickOtelServiceNameFromEnv(cfg Config) string {
	if s := os.Getenv("OTEL_SERVICE_NAME"); s != "" {
		return s
	}
	if cfg.Service.ServerGroup != "" {
		return cfg.Service.ServerGroup
	}
	return cfg.Service.NodeName
}
