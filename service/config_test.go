package service

import (
	"errors"
	"testing"

	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestConfig_Validate_Matrix(t *testing.T) {
	withDB := func() db.Config { return db.Config{User: "u", Database: "d"} }
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"empty is fine", Config{}, ""},
		{"DB only is fine", Config{DB: withDB()}, ""},
		{"NATS only is fine", Config{NATS: NATSConfig{URL: "nats://x"}}, ""},
		{"APIMap only is fine", Config{APIMap: APIMapConfig{Path: "x.yaml"}}, ""},
		{"Auth without DB fails", Config{Auth: AuthConfig{PrivateKeyPEM: "pem"}}, CodeAuthNeedsDB},
		{"Auth with DB is fine", Config{DB: withDB(), Auth: AuthConfig{PrivateKeyPEM: "pem"}}, ""},
		{"everything is fine", Config{
			DB:     withDB(),
			Auth:   AuthConfig{PrivateKeyPEM: "pem"},
			NATS:   NATSConfig{URL: "nats://x"},
			APIMap: APIMapConfig{Path: "x.yaml"},
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
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
