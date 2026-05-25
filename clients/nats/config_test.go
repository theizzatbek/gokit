package natsclient

import (
	"errors"
	"testing"
	"time"

	"github.com/theizzatbek/fibermap/errs"
)

func TestConfig_ValidateOK(t *testing.T) {
	cfg := Config{URL: "nats://localhost:4222"}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestConfig_ValidateRequiresURL(t *testing.T) {
	cfg := Config{}
	err := cfg.validate()
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		t.Fatalf("expected Validation err, got %v", err)
	}
}

func TestConfig_ValidateRejectsAuthAmbiguous(t *testing.T) {
	cases := []Config{
		{URL: "nats://x", Token: "t", User: "u", Password: "p"},
		{URL: "nats://x", Token: "t", CredsFile: "/tmp/c"},
		{URL: "nats://x", CredsFile: "/tmp/c", NKeySeed: "s"},
	}
	for i, cfg := range cases {
		err := cfg.validate()
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != CodeAuthAmbiguous {
			t.Errorf("case %d: expected auth_ambiguous, got %v", i, err)
		}
	}
}

func TestConfig_ValidateUserRequiresPassword(t *testing.T) {
	cfg := Config{URL: "nats://x", User: "u"}
	err := cfg.validate()
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != CodeAuthAmbiguous {
		t.Fatalf("expected auth_ambiguous for lone User, got %v", err)
	}
}

func TestConfig_DefaultsApplied(t *testing.T) {
	cfg := Config{URL: "nats://x"}
	cfg.applyDefaults()
	if cfg.Timeout != 5*time.Second {
		t.Errorf("Timeout default = %v, want 5s", cfg.Timeout)
	}
	if cfg.MaxReconnects != -1 {
		t.Errorf("MaxReconnects default = %d, want -1", cfg.MaxReconnects)
	}
	if cfg.ReconnectWait != 2*time.Second {
		t.Errorf("ReconnectWait default = %v, want 2s", cfg.ReconnectWait)
	}
	if cfg.Name == "" {
		t.Errorf("Name default empty")
	}
}
