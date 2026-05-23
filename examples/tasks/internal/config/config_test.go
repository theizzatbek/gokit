package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// envKeys must list every env var Config reads. Used to wipe the
// environment in TestLoad_Defaults so the test isn't sensitive to
// whatever the developer happens to export in their shell.
var envKeys = []string{
	"ADDR", "SHUTDOWN_TIMEOUT", "BODY_LIMIT",
	"LOG_LEVEL", "LOG_FORMAT",
	"CORS_ORIGINS", "CORS_METHODS",
	"RATE_LIMIT_MAX", "RATE_LIMIT_EXPIRATION",
	"ENV", "API_BASE_URL",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "")
		// t.Setenv to "" still leaves the var set (with empty value),
		// which envconfig treats as explicit empty for strings. Unset
		// explicitly so envDefault kicks in.
		_ = unsetenvFunc(k)
	}
}

// unsetenvFunc is os.Unsetenv as a var so the test file doesn't have to
// import os directly twice.
var unsetenvFunc = func(k string) error {
	return os.Unsetenv(k)
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Addr has no envDefault — empty means "let fibermap.Run resolve
	// $PORT or fall back to :3000".
	if cfg.Addr != "" {
		t.Errorf("Addr = %q, want empty", cfg.Addr)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
	if cfg.BodyLimit != 1048576 {
		t.Errorf("BodyLimit = %d, want 1048576", cfg.BodyLimit)
	}
	if cfg.LogLevel != "info" || cfg.LogFormat != "json" {
		t.Errorf("Log = (%q,%q), want (info,json)", cfg.LogLevel, cfg.LogFormat)
	}
	if len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "*" {
		t.Errorf("CORSOrigins = %v, want [*]", cfg.CORSOrigins)
	}
	wantMethods := []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"}
	if len(cfg.CORSMethods) != len(wantMethods) {
		t.Fatalf("CORSMethods = %v, want %v", cfg.CORSMethods, wantMethods)
	}
	for i, m := range wantMethods {
		if cfg.CORSMethods[i] != m {
			t.Errorf("CORSMethods[%d] = %q, want %q", i, cfg.CORSMethods[i], m)
		}
	}
	if cfg.RateLimitMax != 100 || cfg.RateLimitExpiration != time.Minute {
		t.Errorf("RateLimit = (%d, %v), want (100, 1m)", cfg.RateLimitMax, cfg.RateLimitExpiration)
	}
	if cfg.Env != "development" {
		t.Errorf("Env = %q, want development", cfg.Env)
	}
	if cfg.APIBaseURL != "" {
		t.Errorf("APIBaseURL = %q, want empty", cfg.APIBaseURL)
	}
}

func TestLoad_OverrideFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("ADDR", ":8080")
	t.Setenv("SHUTDOWN_TIMEOUT", "30s")
	t.Setenv("BODY_LIMIT", "4194304")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("CORS_ORIGINS", "https://a.example,https://b.example")
	t.Setenv("CORS_METHODS", "GET,POST")
	t.Setenv("RATE_LIMIT_MAX", "500")
	t.Setenv("RATE_LIMIT_EXPIRATION", "2m")
	t.Setenv("ENV", "production")
	t.Setenv("API_BASE_URL", "https://api.example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v", cfg.ShutdownTimeout)
	}
	if cfg.BodyLimit != 4194304 {
		t.Errorf("BodyLimit = %d", cfg.BodyLimit)
	}
	if cfg.LogLevel != "debug" || cfg.LogFormat != "text" {
		t.Errorf("Log = (%q,%q)", cfg.LogLevel, cfg.LogFormat)
	}
	if len(cfg.CORSOrigins) != 2 || cfg.CORSOrigins[0] != "https://a.example" {
		t.Errorf("CORSOrigins = %v", cfg.CORSOrigins)
	}
	if cfg.RateLimitMax != 500 || cfg.RateLimitExpiration != 2*time.Minute {
		t.Errorf("RateLimit = (%d, %v)", cfg.RateLimitMax, cfg.RateLimitExpiration)
	}
	if cfg.Env != "production" {
		t.Errorf("Env = %q", cfg.Env)
	}
	if cfg.APIBaseURL != "https://api.example.com" {
		t.Errorf("APIBaseURL = %q", cfg.APIBaseURL)
	}
}

func TestLoad_RejectsBadLogLevel(t *testing.T) {
	clearEnv(t)
	t.Setenv("LOG_LEVEL", "verbose")
	_, err := Load()
	if err == nil {
		t.Fatal("Load: want error, got nil")
	}
	if !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Errorf("err = %v, want it to mention LOG_LEVEL", err)
	}
}

func TestLoad_RejectsBadEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("ENV", "prod")
	_, err := Load()
	if err == nil {
		t.Fatal("Load: want error, got nil")
	}
	if !strings.Contains(err.Error(), "ENV") {
		t.Errorf("err = %v, want it to mention ENV", err)
	}
}

func TestLoad_RejectsZeroBodyLimit(t *testing.T) {
	clearEnv(t)
	t.Setenv("BODY_LIMIT", "0")
	_, err := Load()
	if err == nil {
		t.Fatal("Load: want error, got nil")
	}
	if !strings.Contains(err.Error(), "BODY_LIMIT") {
		t.Errorf("err = %v, want it to mention BODY_LIMIT", err)
	}
}
