package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestSigner_Sign_Deterministic(t *testing.T) {
	s := &Signer{}
	body := []byte(`{"a":1}`)
	secret := "shhh"
	now := time.Unix(1700000000, 0)

	header, err := s.Sign(body, secret, now)
	if err != nil {
		t.Fatalf("Sign returned err: %v", err)
	}
	if !strings.HasPrefix(header, "t=1700000000,v1=") {
		t.Fatalf("unexpected prefix: %q", header)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("1700000000."))
	mac.Write(body)
	expected := "t=1700000000,v1=" + hex.EncodeToString(mac.Sum(nil))
	if header != expected {
		t.Fatalf("\nwant: %s\ngot:  %s", expected, header)
	}
}

func TestSigner_Sign_EmptySecret(t *testing.T) {
	s := &Signer{}
	_, err := s.Sign([]byte("{}"), "", time.Now())
	if err == nil {
		t.Fatal("expected error on empty secret")
	}
}
