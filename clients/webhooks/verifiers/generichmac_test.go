package verifiers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"
)

func sigHex(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestGenericHMAC_OK(t *testing.T) {
	v, err := NewGenericHMAC(GenericHMACConfig{
		Secret:          []byte("k"),
		SignatureHeader: "X-Sig",
		Algo:            HashSHA256,
		Encoding:        EncodingHex,
		Prefix:          "sha256=",
	})
	if err != nil {
		t.Fatalf("NewGenericHMAC: %v", err)
	}
	body := []byte("hello")
	headers := map[string][]string{"X-Sig": {"sha256=" + sigHex("k", "hello")}}
	if err := v.Verify(headers, body, time.Now()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestGenericHMAC_Mismatch(t *testing.T) {
	v, _ := NewGenericHMAC(GenericHMACConfig{
		Secret:          []byte("k"),
		SignatureHeader: "X-Sig",
		Algo:            HashSHA256,
		Encoding:        EncodingHex,
	})
	headers := map[string][]string{"X-Sig": {sigHex("WRONG", "hello")}}
	if err := v.Verify(headers, []byte("hello"), time.Now()); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestGenericHMAC_MissingHeader(t *testing.T) {
	v, _ := NewGenericHMAC(GenericHMACConfig{
		Secret:          []byte("k"),
		SignatureHeader: "X-Sig",
		Algo:            HashSHA256,
		Encoding:        EncodingHex,
	})
	if err := v.Verify(map[string][]string{}, []byte("hello"), time.Now()); err == nil {
		t.Fatal("expected missing-header error")
	}
}

func TestGenericHMAC_TimestampWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte("hello")
	signedPayload := ts + "." + string(body)

	v, _ := NewGenericHMAC(GenericHMACConfig{
		Secret:          []byte("k"),
		SignatureHeader: "X-Sig",
		Algo:            HashSHA256,
		Encoding:        EncodingHex,
		TimestampHeader: "X-Ts",
		TimestampWindow: time.Minute,
	})

	headers := map[string][]string{
		"X-Sig": {sigHex("k", signedPayload)},
		"X-Ts":  {ts},
	}
	if err := v.Verify(headers, body, now.Add(10*time.Second)); err != nil {
		t.Fatalf("within window: %v", err)
	}

	if err := v.Verify(headers, body, now.Add(2*time.Minute)); err == nil {
		t.Fatal("expected timestamp-out-of-window error")
	}
}
