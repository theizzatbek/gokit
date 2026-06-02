package breaker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConfig_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     Config
		wantErr string // expected Code, empty means no error
	}{
		{
			name:    "empty name",
			cfg:     Config{},
			wantErr: CodeInvalidName,
		},
		{
			name:    "negative failure threshold",
			cfg:     Config{Name: "x", FailureThreshold: -1},
			wantErr: CodeInvalidFailureThreshold,
		},
		{
			name:    "negative minimum requests",
			cfg:     Config{Name: "x", MinimumRequests: -1},
			wantErr: CodeInvalidMinimumRequests,
		},
		{
			name:    "minimum below threshold",
			cfg:     Config{Name: "x", FailureThreshold: 50, MinimumRequests: 10},
			wantErr: CodeInvalidMinimumRequests,
		},
		{
			name:    "negative window duration",
			cfg:     Config{Name: "x", WindowDuration: -1},
			wantErr: CodeInvalidWindow,
		},
		{
			name:    "negative window size",
			cfg:     Config{Name: "x", WindowSize: -1},
			wantErr: CodeInvalidWindow,
		},
		{
			name:    "negative open interval",
			cfg:     Config{Name: "x", OpenInterval: -time.Second},
			wantErr: CodeInvalidOpenInterval,
		},
		{
			name:    "negative half-open probes",
			cfg:     Config{Name: "x", HalfOpenMaxProbes: -1},
			wantErr: CodeInvalidHalfOpenMaxProbes,
		},
		{
			name:    "all zero values are valid (defaults applied)",
			cfg:     Config{Name: "x"},
			wantErr: "",
		},
		{
			name:    "fully populated",
			cfg:     Config{Name: "x", FailureThreshold: 5, MinimumRequests: 5, WindowDuration: time.Second, WindowSize: 5, OpenInterval: time.Second, HalfOpenMaxProbes: 1},
			wantErr: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			var be *Error
			if !errors.As(err, &be) {
				t.Fatalf("want *Error, got %T (%v)", err, err)
			}
			if be.Code != tc.wantErr {
				t.Fatalf("Code = %q, want %q", be.Code, tc.wantErr)
			}
		})
	}
}

func TestConfig_ApplyDefaults(t *testing.T) {
	t.Parallel()
	cfg := Config{Name: "x"}
	cfg.applyDefaults()
	if cfg.FailureThreshold != defaultFailureThreshold {
		t.Errorf("FailureThreshold = %d, want %d", cfg.FailureThreshold, defaultFailureThreshold)
	}
	if cfg.MinimumRequests != defaultMinimumRequests {
		t.Errorf("MinimumRequests = %d, want %d", cfg.MinimumRequests, defaultMinimumRequests)
	}
	if cfg.WindowDuration != defaultWindowDuration {
		t.Errorf("WindowDuration = %v, want %v", cfg.WindowDuration, defaultWindowDuration)
	}
	if cfg.WindowSize != defaultWindowSize {
		t.Errorf("WindowSize = %d, want %d", cfg.WindowSize, defaultWindowSize)
	}
	if cfg.OpenInterval != defaultOpenInterval {
		t.Errorf("OpenInterval = %v, want %v", cfg.OpenInterval, defaultOpenInterval)
	}
	if cfg.HalfOpenMaxProbes != defaultHalfOpenMaxProbes {
		t.Errorf("HalfOpenMaxProbes = %d, want %d", cfg.HalfOpenMaxProbes, defaultHalfOpenMaxProbes)
	}
	if cfg.IsFailure == nil {
		t.Error("IsFailure default not set")
	}
	if cfg.Now == nil {
		t.Error("Now default not set")
	}
}

func TestDefaultIsFailure(t *testing.T) {
	t.Parallel()
	if defaultIsFailure(nil) {
		t.Error("nil err must be success")
	}
	if defaultIsFailure(context.Canceled) {
		t.Error("context.Canceled must be success (user cancel != upstream failure)")
	}
	if !defaultIsFailure(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded must be failure (slow upstream)")
	}
	if !defaultIsFailure(errors.New("boom")) {
		t.Error("arbitrary error must be failure")
	}
}
