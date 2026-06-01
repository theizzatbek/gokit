package verifiers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestGitHub_OK(t *testing.T) {
	v := NewGitHub([]byte("ghsecret"))
	body := []byte(`{"action":"opened"}`)
	mac := hmac.New(sha256.New, []byte("ghsecret"))
	mac.Write(body)
	headers := map[string][]string{
		"X-Hub-Signature-256": {"sha256=" + hex.EncodeToString(mac.Sum(nil))},
	}
	if err := v.Verify(headers, body, time.Now()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestGitHub_Mismatch(t *testing.T) {
	v := NewGitHub([]byte("ghsecret"))
	headers := map[string][]string{"X-Hub-Signature-256": {"sha256=deadbeef"}}
	if err := v.Verify(headers, []byte("body"), time.Now()); err == nil {
		t.Fatal("expected mismatch error")
	}
}
