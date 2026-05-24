package auth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func ed25519PEM(t *testing.T) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func ed25519PublicPEM(t *testing.T, pub ed25519.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func TestGenerateEd25519Key_ProducesActiveAndVerifyEntry(t *testing.T) {
	ks, err := GenerateEd25519Key("k1")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if ks.active.KID != "k1" {
		t.Fatalf("active.KID = %q, want %q", ks.active.KID, "k1")
	}
	if ks.active.Alg != "EdDSA" {
		t.Fatalf("active.Alg = %q, want EdDSA", ks.active.Alg)
	}
	if _, ok := ks.verify["k1"]; !ok {
		t.Fatalf("verify map missing k1: %v", ks.verify)
	}
	if _, ok := ks.active.Priv.(ed25519.PrivateKey); !ok {
		t.Fatalf("active.Priv is %T, want ed25519.PrivateKey", ks.active.Priv)
	}
}

func TestLoadKeysFromPEM_PrivatePopulatesBothSigningAndVerify(t *testing.T) {
	priv := ed25519PEM(t)
	ks, err := LoadKeysFromPEM("k1", map[string][]byte{"k1": priv})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if ks.active.Priv == nil {
		t.Fatalf("expected priv key, got nil")
	}
	if _, ok := ks.verify["k1"]; !ok {
		t.Fatalf("verify map should contain k1")
	}
}

func TestLoadKeysFromPEM_PublicOnlyIsVerifyOnly(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pemBytes := ed25519PublicPEM(t, pub)
	ks, err := LoadKeysFromPEM("", map[string][]byte{"k1": pemBytes})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	entry := ks.verify["k1"]
	if entry.Priv != nil {
		t.Fatalf("public-only key has Priv: %v", entry.Priv)
	}
	if entry.Pub == nil {
		t.Fatalf("public-only key has no Pub")
	}
}

func TestLoadKeysFromPEM_RotationPreservesOldVerifier(t *testing.T) {
	old := ed25519PEM(t)
	new_ := ed25519PEM(t)
	ks, err := LoadKeysFromPEM("k2", map[string][]byte{"k1": old, "k2": new_})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if ks.active.KID != "k2" {
		t.Fatalf("active.KID = %q, want k2", ks.active.KID)
	}
	if _, ok := ks.verify["k1"]; !ok {
		t.Fatalf("old key k1 missing from verify map")
	}
	if _, ok := ks.verify["k2"]; !ok {
		t.Fatalf("new key k2 missing from verify map")
	}
}

func TestLoadKeysFromPEM_ActiveMustHavePrivate(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pemBytes := ed25519PublicPEM(t, pub)
	_, err := LoadKeysFromPEM("k1", map[string][]byte{"k1": pemBytes})
	if err == nil {
		t.Fatalf("expected error: active kid points to a public-only key")
	}
}

func TestLoadKeysFromPEM_ECDSAPrivate(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	ks, err := LoadKeysFromPEM("k1", map[string][]byte{"k1": pemBytes})
	if err != nil {
		t.Fatalf("load ECDSA: %v", err)
	}
	if ks.active.Alg != "ES256" {
		t.Fatalf("active.Alg = %q, want ES256", ks.active.Alg)
	}
}
