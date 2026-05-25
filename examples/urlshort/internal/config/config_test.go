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
		{"missing MICROLINK_BASE_URL", func(c *Config) { c.MicrolinkBaseURL = "" }, "urlshort_missing_microlink_base_url"},
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

func validBase() Config {
	c := Config{
		MicrolinkBaseURL: "https://x",
		ShortURLBase:     "http://localhost:3000",
	}
	return c
}
