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
		{"testdata/invalid_stream_missing_name.yaml", CodeStreamMissingName},
		{"testdata/invalid_stream_missing_subjects.yaml", CodeStreamMissingSubjects},
		{"testdata/invalid_stream_duplicate_name.yaml", CodeStreamDuplicateName},
		{"testdata/invalid_stream_storage.yaml", CodeStreamInvalidStorage},
		{"testdata/invalid_stream_retention.yaml", CodeStreamInvalidRetention},
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

func TestParseBytes_StreamsExplicit(t *testing.T) {
	b, err := os.ReadFile("testdata/streams_explicit.yaml")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	cfg, err := parseBytes(b, nil)
	if err != nil {
		t.Fatalf("parseBytes: %v", err)
	}
	if cfg.Streams.Auto {
		t.Fatal("expected Auto=false")
	}
	if len(cfg.Streams.List) != 1 || cfg.Streams.List[0].Name != "ORDERS" {
		t.Fatalf("streams: %+v", cfg.Streams)
	}
}

func TestParseBytes_StreamsAuto(t *testing.T) {
	b, err := os.ReadFile("testdata/streams_auto.yaml")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	cfg, err := parseBytes(b, nil)
	if err != nil {
		t.Fatalf("parseBytes: %v", err)
	}
	if !cfg.Streams.Auto {
		t.Fatal("expected Auto=true")
	}
	if len(cfg.Streams.List) != 0 {
		t.Fatalf("expected empty List, got %+v", cfg.Streams.List)
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
