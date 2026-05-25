package httpc

import (
	"errors"
	"testing"
	"time"

	xerrs "github.com/theizzatbek/fibermap/errs"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string // empty = no error; otherwise expected Code
	}{
		{"zero values are valid (defaults will kick in)", Config{}, ""},
		{"only Timeout set", Config{Timeout: time.Second}, ""},
		{"negative Timeout", Config{Timeout: -1}, CodeInvalidTimeout},
		{"negative MaxRetries", Config{MaxRetries: -1}, CodeInvalidMaxRetries},
		{"BackoffBase negative", Config{BackoffBase: -1}, CodeInvalidBackoff},
		{"BackoffMax < BackoffBase", Config{BackoffBase: 10 * time.Millisecond, BackoffMax: time.Millisecond}, CodeInvalidBackoff},
		{"BackoffMax negative", Config{BackoffMax: -1}, CodeInvalidBackoff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate() = nil, want code %q", tt.wantErr)
			}
			var e *xerrs.Error
			if !errors.As(err, &e) {
				t.Fatalf("validate() = %v (type %T), want *xerrs.Error", err, err)
			}
			if e.Code != tt.wantErr {
				t.Errorf("Code = %q, want %q", e.Code, tt.wantErr)
			}
		})
	}
}

func TestConfig_ApplyDefaults(t *testing.T) {
	var c Config
	c.applyDefaults()
	if c.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", c.Timeout)
	}
	// MaxRetries deliberately stays at 0 (zero means "no retries").
	if c.BackoffBase != 100*time.Millisecond {
		t.Errorf("BackoffBase = %v, want 100ms", c.BackoffBase)
	}
	if c.BackoffMax != 5*time.Second {
		t.Errorf("BackoffMax = %v, want 5s", c.BackoffMax)
	}
}

func TestConfig_ApplyDefaults_PreservesUserValues(t *testing.T) {
	c := Config{
		Timeout:     2 * time.Second,
		MaxRetries:  7,
		BackoffBase: 50 * time.Millisecond,
		BackoffMax:  time.Second,
	}
	c.applyDefaults()
	if c.Timeout != 2*time.Second {
		t.Errorf("Timeout overridden")
	}
	if c.MaxRetries != 7 {
		t.Errorf("MaxRetries overridden")
	}
}

func TestConfig_MaxRetriesZero_Valid(t *testing.T) {
	// MaxRetries: 0 explicitly disables retries — must NOT be rejected.
	cfg := Config{MaxRetries: 0}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() = %v, want nil (MaxRetries:0 disables retries)", err)
	}
}
