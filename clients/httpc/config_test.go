package httpc

import (
	"errors"
	"testing"
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
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
		{"MaxRetries -1 is the disable sentinel", Config{MaxRetries: -1}, ""},
		{"MaxRetries < -1 invalid", Config{MaxRetries: -2}, CodeInvalidMaxRetries},
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
	if c.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3 (zero gets default)", c.MaxRetries)
	}
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

func TestConfig_MaxRetriesNegativeOne_DisablesRetries(t *testing.T) {
	cfg := Config{MaxRetries: -1}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() = %v, want nil (-1 is the disable sentinel)", err)
	}
	cfg.applyDefaults()
	if cfg.MaxRetries != 0 {
		t.Errorf("after applyDefaults: MaxRetries = %d, want 0 (mapped from -1)", cfg.MaxRetries)
	}
}
