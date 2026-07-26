package svckit

import (
	"errors"
	"testing"

	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestConfigValidate_AuthNeedsDB(t *testing.T) {
	cfg := Config{Auth: AuthConfig{PrivateKeyPEM: "pem"}}

	err := cfg.Validate()

	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeAuthNeedsDB {
		t.Fatalf("want Code=%q, got %#v", CodeAuthNeedsDB, err)
	}
}

func TestConfigValidate_TLSHalfConfigured(t *testing.T) {
	cfg := Config{}
	cfg.Service.TLSCertFile = "cert.pem" // ключ не задан

	err := cfg.Validate()

	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeTLSConfigIncomplete {
		t.Fatalf("want Code=%q, got %#v", CodeTLSConfigIncomplete, err)
	}
}

func TestConfigValidate_OK(t *testing.T) {
	cfg := Config{
		DB:   db.Config{User: "app"},
		Auth: AuthConfig{PrivateKeyPEM: "pem"},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}
