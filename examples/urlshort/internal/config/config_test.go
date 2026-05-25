package config

import (
	"errors"
	"testing"

	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestValidate_Matrix(t *testing.T) {
	tests := []struct {
		name    string
		mod     func(*Config)
		wantErr string
	}{
		{"all set", func(c *Config) {}, ""},
		{"missing NATS_URL", func(c *Config) { c.NATSURL = "" }, "urlshort_missing_nats_url"},
		{"missing MICROLINK_BASE_URL", func(c *Config) { c.MicrolinkBaseURL = "" }, "urlshort_missing_microlink_base_url"},
		{"missing JWT_PRIVATE_KEY_PEM", func(c *Config) { c.JWTPrivateKeyPEM = "" }, "urlshort_missing_jwt_private_key"},
		{"missing SHORT_URL_BASE", func(c *Config) { c.ShortURLBase = "" }, "urlshort_missing_short_url_base"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validBase()
			tt.mod(&c)
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			var e *xerrs.Error
			if !errors.As(err, &e) || e.Code != tt.wantErr {
				t.Errorf("err = %v, want code %q", err, tt.wantErr)
			}
		})
	}
}

func TestApplyDefaults(t *testing.T) {
	c := Config{}
	c.ApplyDefaults()
	if c.Addr != ":3000" {
		t.Errorf("Addr = %q, want :3000", c.Addr)
	}
	if c.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", c.LogLevel)
	}
}

func TestApplyDefaults_PreservesUserValues(t *testing.T) {
	c := Config{Addr: ":8080", LogLevel: "debug"}
	c.ApplyDefaults()
	if c.Addr != ":8080" {
		t.Errorf("Addr overridden = %q", c.Addr)
	}
	if c.LogLevel != "debug" {
		t.Errorf("LogLevel overridden = %q", c.LogLevel)
	}
}

func validBase() Config {
	return Config{
		NATSURL:          "nats://x",
		MicrolinkBaseURL: "https://x",
		JWTPrivateKeyPEM: "-----BEGIN PRIVATE KEY-----\nfake\n-----END PRIVATE KEY-----",
		ShortURLBase:     "http://localhost:3000",
		Addr:             ":3000",
		LogLevel:         "info",
	}
}
