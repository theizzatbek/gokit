package service

import "testing"

// Regression suite for v1.0.1 P2-13 (LicenseKit followup): env-
// driven Sentry / OTel auto-enable. The contract:
//
//   - SENTRY_DSN     → applies when caller did not pass WithSentry.
//   - OTEL_EXPORTER_OTLP_*ENDPOINT → applies when caller did not pass
//                                    WithOtel AND OTEL_SDK_DISABLED!=true.
//   - OTEL_SERVICE_NAME / cfg.Service.ServerGroup / NodeName resolve
//     the OTel service.name in that order.
//   - An empty otelServiceName + no env → no auto-enable.
//
// All tests use t.Setenv so process-wide state is restored on exit
// and parallel-safe.

func TestApplyEnvDefaults_NoEnv_NoCallerOpts_LeavesAllEmpty(t *testing.T) {
	t.Setenv("SENTRY_DSN", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_SERVICE_NAME", "")

	o := &options{}
	applyEnvDefaults(o, Config{})

	if o.sentryDSN != "" {
		t.Errorf("sentryDSN = %q, want empty", o.sentryDSN)
	}
	if o.otelServiceName != "" {
		t.Errorf("otelServiceName = %q, want empty", o.otelServiceName)
	}
}

func TestApplyEnvDefaults_SentryDSNEnv_PopulatesSlot(t *testing.T) {
	t.Setenv("SENTRY_DSN", "https://key@sentry.example/1")

	o := &options{}
	applyEnvDefaults(o, Config{})

	if want := "https://key@sentry.example/1"; o.sentryDSN != want {
		t.Errorf("sentryDSN = %q, want %q", o.sentryDSN, want)
	}
}

func TestApplyEnvDefaults_SentryDSNEnv_DefersToCallerOpt(t *testing.T) {
	t.Setenv("SENTRY_DSN", "https://from-env@sentry.example/1")

	// Caller already wired WithSentry with their own DSN — the env
	// MUST NOT override.
	o := &options{sentryDSN: "https://from-caller@sentry.example/2"}
	applyEnvDefaults(o, Config{})

	if want := "https://from-caller@sentry.example/2"; o.sentryDSN != want {
		t.Errorf("sentryDSN = %q, want caller-supplied %q", o.sentryDSN, want)
	}
}

func TestApplyEnvDefaults_OtelEndpointEnv_UsesOtelServiceNameEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example:4318")
	t.Setenv("OTEL_SERVICE_NAME", "orders-api")

	o := &options{}
	applyEnvDefaults(o, Config{})

	if o.otelServiceName != "orders-api" {
		t.Errorf("otelServiceName = %q, want orders-api (from OTEL_SERVICE_NAME)", o.otelServiceName)
	}
}

func TestApplyEnvDefaults_OtelEndpointEnv_FallsBackToServerGroup(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "https://otel.example:4318/v1/traces")
	t.Setenv("OTEL_SERVICE_NAME", "")

	cfg := Config{}
	cfg.Service.ServerGroup = "billing"
	cfg.Service.NodeName = "ip-10-0-0-5"

	o := &options{}
	applyEnvDefaults(o, cfg)

	if o.otelServiceName != "billing" {
		t.Errorf("otelServiceName = %q, want billing (ServerGroup wins over NodeName)", o.otelServiceName)
	}
}

func TestApplyEnvDefaults_OtelEndpointEnv_FallsBackToNodeName(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example:4318")
	t.Setenv("OTEL_SERVICE_NAME", "")

	cfg := Config{}
	cfg.Service.NodeName = "ip-10-0-0-5"

	o := &options{}
	applyEnvDefaults(o, cfg)

	if o.otelServiceName != "ip-10-0-0-5" {
		t.Errorf("otelServiceName = %q, want ip-10-0-0-5 (NodeName last resort)", o.otelServiceName)
	}
}

func TestApplyEnvDefaults_OtelEndpointEnv_NoNameSources_SkipsAutoEnable(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example:4318")
	t.Setenv("OTEL_SERVICE_NAME", "")

	// cfg.Service has neither ServerGroup nor NodeName — we have no
	// good default to invent, so auto-enable must defer.
	o := &options{}
	applyEnvDefaults(o, Config{})

	if o.otelServiceName != "" {
		t.Errorf("otelServiceName = %q, want empty (no name source)", o.otelServiceName)
	}
}

func TestApplyEnvDefaults_OtelSDKDisabled_SkipsRegardlessOfEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example:4318")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("OTEL_SERVICE_NAME", "would-have-won")

	cfg := Config{}
	cfg.Service.ServerGroup = "billing"

	o := &options{}
	applyEnvDefaults(o, cfg)

	if o.otelServiceName != "" {
		t.Errorf("otelServiceName = %q, want empty (OTEL_SDK_DISABLED kill switch)", o.otelServiceName)
	}
}

func TestApplyEnvDefaults_OtelEndpointEnv_DefersToCallerOpt(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example:4318")
	t.Setenv("OTEL_SERVICE_NAME", "would-be-overridden")

	// Caller already wired WithOtel with their own name.
	o := &options{otelServiceName: "caller-chose"}
	applyEnvDefaults(o, Config{})

	if o.otelServiceName != "caller-chose" {
		t.Errorf("otelServiceName = %q, want caller-supplied caller-chose", o.otelServiceName)
	}
}
