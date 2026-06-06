package service

import (
	"strings"
	"testing"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/clients/apimap"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/clients/ratelimit"
	redisclient "github.com/theizzatbek/gokit/clients/redis"
	s3client "github.com/theizzatbek/gokit/clients/s3"
	"github.com/theizzatbek/gokit/clients/webhooks"
	"github.com/theizzatbek/gokit/cronmap"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/outbox"
)

// expectMustPanic verifies that mustFn panics with a message that
// contains the named subsystem (so the operator sees "MustDB", not a
// generic nil-pointer crash).
func expectMustPanic(t *testing.T, name string, mustFn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("%s: expected panic, got none", name)
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("%s: panic value is %T, want string", name, r)
		}
		if !strings.Contains(msg, name) {
			t.Errorf("%s: panic message missing %q: %q", name, name, msg)
		}
	}()
	mustFn()
}

// TestAccessors_NilSubsystem_OptionalReturnsFalse covers the
// (nil, false) contract for every optional accessor on a
// zero-value Service.
func TestAccessors_NilSubsystem_OptionalReturnsFalse(t *testing.T) {
	s := &Service[struct{}, struct{}]{}

	if got, ok := s.OptionalDB(); got != nil || ok {
		t.Errorf("OptionalDB = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalAuth(); got != nil || ok {
		t.Errorf("OptionalAuth = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalNATS(); got != nil || ok {
		t.Errorf("OptionalNATS = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalRedis(); got != nil || ok {
		t.Errorf("OptionalRedis = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalNATSMap(); got != nil || ok {
		t.Errorf("OptionalNATSMap = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalAPIMap(); got != nil || ok {
		t.Errorf("OptionalAPIMap = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalHasher(); got != nil || ok {
		t.Errorf("OptionalHasher = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalOutbox(); got != nil || ok {
		t.Errorf("OptionalOutbox = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalS3(); got != nil || ok {
		t.Errorf("OptionalS3 = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalCronMap(); got != nil || ok {
		t.Errorf("OptionalCronMap = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalRateLimiter(); got != nil || ok {
		t.Errorf("OptionalRateLimiter = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalWebhooksWorker(); got != nil || ok {
		t.Errorf("OptionalWebhooksWorker = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalWebhooksFanout(); got != nil || ok {
		t.Errorf("OptionalWebhooksFanout = (%v, %v), want (nil, false)", got, ok)
	}
}

// TestAccessors_NilSubsystem_MustPanics covers Must* on the same
// zero-value Service — each must panic and the message must name
// the missing subsystem.
func TestAccessors_NilSubsystem_MustPanics(t *testing.T) {
	s := &Service[struct{}, struct{}]{}

	expectMustPanic(t, "MustDB", func() { _ = s.MustDB() })
	expectMustPanic(t, "MustAuth", func() { _ = s.MustAuth() })
	expectMustPanic(t, "MustNATS", func() { _ = s.MustNATS() })
	expectMustPanic(t, "MustRedis", func() { _ = s.MustRedis() })
	expectMustPanic(t, "MustNATSMap", func() { _ = s.MustNATSMap() })
	expectMustPanic(t, "MustAPIMap", func() { _ = s.MustAPIMap() })
	expectMustPanic(t, "MustHasher", func() { _ = s.MustHasher() })
	expectMustPanic(t, "MustOutbox", func() { _ = s.MustOutbox() })
	expectMustPanic(t, "MustS3", func() { _ = s.MustS3() })
	expectMustPanic(t, "MustCronMap", func() { _ = s.MustCronMap() })
	expectMustPanic(t, "MustRateLimiter", func() { _ = s.MustRateLimiter() })
	expectMustPanic(t, "MustWebhooksWorker", func() { _ = s.MustWebhooksWorker() })
	expectMustPanic(t, "MustWebhooksFanout", func() { _ = s.MustWebhooksFanout() })
}

// TestAccessors_PopulatedSubsystem_ReturnsValueAndOK covers the
// happy path: each Optional* returns (value, true) and each Must*
// returns the value without panicking when the underlying field
// is set. Uses zero-value subsystem instances since the accessors
// inspect only the nil/non-nil shape.
func TestAccessors_PopulatedSubsystem_ReturnsValueAndOK(t *testing.T) {
	s := &Service[struct{}, struct{}]{
		DB:             &db.DB{},
		Auth:           &auth.Auth[struct{}]{},
		NATS:           &natsclient.Client{},
		Redis:          &redisclient.Client{},
		NATSMap:        &natsmap.Runtime{},
		APIMap:         &apimap.Client{},
		Hasher:         &auth.Hasher{},
		Outbox:         &outbox.Worker{},
		S3:             &s3client.Client{},
		CronMap:        &cronmap.Runtime{},
		RateLimiter:    &ratelimit.Redis{},
		WebhooksWorker: &webhooks.Worker{},
		WebhooksFanout: &webhooks.Fanout{},
	}

	if _, ok := s.OptionalDB(); !ok {
		t.Error("OptionalDB ok = false, want true")
	}
	if _, ok := s.OptionalAuth(); !ok {
		t.Error("OptionalAuth ok = false, want true")
	}
	if _, ok := s.OptionalNATS(); !ok {
		t.Error("OptionalNATS ok = false, want true")
	}
	if _, ok := s.OptionalRedis(); !ok {
		t.Error("OptionalRedis ok = false, want true")
	}
	if _, ok := s.OptionalNATSMap(); !ok {
		t.Error("OptionalNATSMap ok = false, want true")
	}
	if _, ok := s.OptionalAPIMap(); !ok {
		t.Error("OptionalAPIMap ok = false, want true")
	}
	if _, ok := s.OptionalHasher(); !ok {
		t.Error("OptionalHasher ok = false, want true")
	}
	if _, ok := s.OptionalOutbox(); !ok {
		t.Error("OptionalOutbox ok = false, want true")
	}
	if _, ok := s.OptionalS3(); !ok {
		t.Error("OptionalS3 ok = false, want true")
	}
	if _, ok := s.OptionalCronMap(); !ok {
		t.Error("OptionalCronMap ok = false, want true")
	}
	if _, ok := s.OptionalRateLimiter(); !ok {
		t.Error("OptionalRateLimiter ok = false, want true")
	}
	if _, ok := s.OptionalWebhooksWorker(); !ok {
		t.Error("OptionalWebhooksWorker ok = false, want true")
	}
	if _, ok := s.OptionalWebhooksFanout(); !ok {
		t.Error("OptionalWebhooksFanout ok = false, want true")
	}

	// Must* should not panic when populated.
	mustFns := []struct {
		name string
		fn   func()
	}{
		{"MustDB", func() { _ = s.MustDB() }},
		{"MustAuth", func() { _ = s.MustAuth() }},
		{"MustNATS", func() { _ = s.MustNATS() }},
		{"MustRedis", func() { _ = s.MustRedis() }},
		{"MustNATSMap", func() { _ = s.MustNATSMap() }},
		{"MustAPIMap", func() { _ = s.MustAPIMap() }},
		{"MustHasher", func() { _ = s.MustHasher() }},
		{"MustOutbox", func() { _ = s.MustOutbox() }},
		{"MustS3", func() { _ = s.MustS3() }},
		{"MustCronMap", func() { _ = s.MustCronMap() }},
		{"MustRateLimiter", func() { _ = s.MustRateLimiter() }},
		{"MustWebhooksWorker", func() { _ = s.MustWebhooksWorker() }},
		{"MustWebhooksFanout", func() { _ = s.MustWebhooksFanout() }},
	}
	for _, m := range mustFns {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked when populated: %v", m.name, r)
				}
			}()
			m.fn()
		}()
	}
}
