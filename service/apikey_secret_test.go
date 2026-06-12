package service

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Tests for v1.1.0 P0-2 service-side decoder: AUTH_APIKEY_HASH_SECRET
// must be accepted in every base64 flavour Go's stdlib knows, decoded
// length must be ≥ 32 bytes (HMAC-SHA256 best practice), and every
// failure path must surface as *errs.Error{Code:
// CodeAuthInvalidAPIKeyHashSecret} at service.New time — never as a
// runtime panic from the APIKey middleware on the first request.

func TestDecodeAPIKeyHashSecret_Empty_NilNil(t *testing.T) {
	got, err := decodeAPIKeyHashSecret("")
	if err != nil {
		t.Fatalf("decodeAPIKeyHashSecret('') err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("decodeAPIKeyHashSecret('') = %x, want nil (signals 'no pepper supplied' to buildAuth)", got)
	}
}

func TestDecodeAPIKeyHashSecret_AcceptsEveryBase64Flavour(t *testing.T) {
	raw := []byte("the-32-byte-secret-value-here-OK")
	if len(raw) != 32 {
		t.Fatalf("test setup: raw secret length %d, expected 32", len(raw))
	}
	cases := map[string]string{
		"std-padded":     base64.StdEncoding.EncodeToString(raw),
		"std-raw":        base64.RawStdEncoding.EncodeToString(raw),
		"url-padded":     base64.URLEncoding.EncodeToString(raw),
		"url-raw":        base64.RawURLEncoding.EncodeToString(raw),
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := decodeAPIKeyHashSecret(encoded)
			if err != nil {
				t.Fatalf("decode %s: %v", encoded, err)
			}
			if string(got) != string(raw) {
				t.Errorf("decoded = %q, want %q", got, raw)
			}
		})
	}
}

func TestDecodeAPIKeyHashSecret_BadBase64_ValidationCode(t *testing.T) {
	_, err := decodeAPIKeyHashSecret("not a valid base64 string!!!")
	if err == nil {
		t.Fatal("expected error on garbage base64, got nil")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *errs.Error, got %T: %v", err, err)
	}
	if e.Code != CodeAuthInvalidAPIKeyHashSecret {
		t.Errorf("Code = %q, want %q", e.Code, CodeAuthInvalidAPIKeyHashSecret)
	}
	if !strings.Contains(e.Error(), "base64") {
		t.Errorf("error message should mention base64 to be actionable, got %q", e.Error())
	}
}

func TestDecodeAPIKeyHashSecret_TooShort_ValidationCode(t *testing.T) {
	// 16 bytes is below the 32-byte HMAC-SHA256 floor.
	encoded := base64.StdEncoding.EncodeToString([]byte("1234567890abcdef"))
	_, err := decodeAPIKeyHashSecret(encoded)
	if err == nil {
		t.Fatal("expected error on 16-byte secret, got nil")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *errs.Error, got %T: %v", err, err)
	}
	if e.Code != CodeAuthInvalidAPIKeyHashSecret {
		t.Errorf("Code = %q, want %q", e.Code, CodeAuthInvalidAPIKeyHashSecret)
	}
	if !strings.Contains(e.Error(), "32") {
		t.Errorf("error should name the required floor (32), got %q", e.Error())
	}
}

func TestDecodeAPIKeyHashSecret_ExactlyFloor_OK(t *testing.T) {
	raw := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") // exactly 32 bytes
	encoded := base64.StdEncoding.EncodeToString(raw)
	got, err := decodeAPIKeyHashSecret(encoded)
	if err != nil {
		t.Fatalf("unexpected error on 32-byte secret: %v", err)
	}
	if len(got) != 32 {
		t.Errorf("got %d bytes, want 32", len(got))
	}
}
