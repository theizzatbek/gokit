package natsmap

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestParseBytes_Happy(t *testing.T) {
	b, err := os.ReadFile("testdata/events.yaml")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	cfg, err := parseBytes(b, nil)
	if err != nil {
		t.Fatalf("parseBytes: %v", err)
	}
	if len(cfg.Subscribers) != 1 || len(cfg.Publishers) != 1 {
		t.Fatalf("want 1 subscriber + 1 publisher, got %d/%d", len(cfg.Subscribers), len(cfg.Publishers))
	}
}

func TestValidate_InvalidFixtures(t *testing.T) {
	cases := []struct {
		path string
		code string
	}{
		{"testdata/invalid_no_entries.yaml", CodeNoEntries},
		{"testdata/invalid_dup_subscriber.yaml", CodeDuplicateSubscriber},
		{"testdata/invalid_dup_publisher.yaml", CodeDuplicatePublisher},
		{"testdata/invalid_missing_name.yaml", CodeMissingName},
		{"testdata/invalid_missing_subject.yaml", CodeMissingSubject},
		{"testdata/invalid_max_in_flight.yaml", CodeInvalidMaxInFlight},
		{"testdata/invalid_max_deliver.yaml", CodeInvalidMaxDeliver},
		{"testdata/invalid_ack_wait.yaml", CodeInvalidAckWait},
		{"testdata/invalid_backoff.yaml", CodeInvalidBackoff},
		{"testdata/invalid_start_from.yaml", CodeInvalidStartFrom},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			b, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			cfg, err := parseBytes(b, nil)
			if err != nil && strings.Contains(err.Error(), tc.code) {
				return
			}
			if err != nil {
				t.Fatalf("parseBytes wrong error: want code %q, got %v", tc.code, err)
			}
			verr := cfg.validate(nil, nil)
			if verr == nil {
				t.Fatalf("validate: want %q, got nil", tc.code)
			}
			if !errorContainsCode(verr, tc.code) {
				t.Fatalf("validate: want code %q in %v", tc.code, verr)
			}
		})
	}
}

func TestParseBytes_PublishersFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/publishers.yaml")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	cfg, err := parseBytes(b, nil)
	if err != nil {
		t.Fatalf("parseBytes: %v", err)
	}
	if len(cfg.Publishers) != 1 {
		t.Fatalf("want 1 publisher, got %d", len(cfg.Publishers))
	}
	if got, want := cfg.Publishers[0].Headers["X-Source"], "invoice-svc"; got != want {
		t.Fatalf("header X-Source: got %q want %q", got, want)
	}
}

func errorContainsCode(err error, code string) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), code) {
		return true
	}
	type joined interface{ Unwrap() []error }
	if j, ok := err.(joined); ok {
		for _, child := range j.Unwrap() {
			if errorContainsCode(child, code) {
				return true
			}
		}
	}
	var w interface{ Unwrap() error }
	if errors.As(err, &w) {
		return errorContainsCode(w.Unwrap(), code)
	}
	return false
}
