package bulkhead

import (
	"errors"
	"testing"
	"time"
)

func TestConfig_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"empty name", Config{MaxConcurrent: 1}, CodeInvalidName},
		{"zero MaxConcurrent", Config{Name: "x"}, CodeInvalidMaxConcurrent},
		{"negative MaxConcurrent", Config{Name: "x", MaxConcurrent: -1}, CodeInvalidMaxConcurrent},
		{"negative MaxQueue", Config{Name: "x", MaxConcurrent: 1, MaxQueue: -1}, CodeInvalidMaxQueue},
		{"negative QueueTimeout", Config{Name: "x", MaxConcurrent: 1, QueueTimeout: -time.Second}, CodeInvalidQueueTimeout},
		{"happy minimal", Config{Name: "x", MaxConcurrent: 1}, ""},
		{"happy populated", Config{Name: "x", MaxConcurrent: 10, MaxQueue: 20, QueueTimeout: 100 * time.Millisecond}, ""},
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
