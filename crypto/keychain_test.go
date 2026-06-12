package crypto

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestNewKeychain_HappyPath(t *testing.T) {
	kc, err := NewKeychain(0x01, map[byte][]byte{
		0x01: mustRand32(t),
	})
	if err != nil {
		t.Fatalf("NewKeychain: %v", err)
	}
	if kc.ActiveKID() != 0x01 {
		t.Errorf("ActiveKID = 0x%02x, want 0x01", kc.ActiveKID())
	}
}

func TestNewKeychain_RejectsEmptyMap(t *testing.T) {
	_, err := NewKeychain(0x01, map[byte][]byte{})
	if err == nil {
		t.Fatal("expected error on empty key map")
	}
	if codeOf(err) != CodeKeychainEmpty {
		t.Errorf("code = %q, want %q", codeOf(err), CodeKeychainEmpty)
	}
}

func TestNewKeychain_RejectsActiveNotInMap(t *testing.T) {
	_, err := NewKeychain(0x02, map[byte][]byte{
		0x01: mustRand32(t),
	})
	if err == nil {
		t.Fatal("expected error on active kid absent from map")
	}
	if codeOf(err) != CodeKeychainNoActive {
		t.Errorf("code = %q, want %q", codeOf(err), CodeKeychainNoActive)
	}
}

func TestNewKeychain_RejectsBadKeyLength(t *testing.T) {
	_, err := NewKeychain(0x01, map[byte][]byte{
		0x01: make([]byte, 16),
	})
	if err == nil {
		t.Fatal("expected error on 16-byte key")
	}
	if codeOf(err) != CodeKeyLength {
		t.Errorf("code = %q, want %q", codeOf(err), CodeKeyLength)
	}
}

func TestNewKeychainFromBase64Map_HappyPath(t *testing.T) {
	raw1 := mustRand32(t)
	raw2 := mustRand32(t)
	kc, err := NewKeychainFromBase64Map(0x02, map[byte]string{
		0x01: base64.StdEncoding.EncodeToString(raw1),
		0x02: base64.RawURLEncoding.EncodeToString(raw2),
	})
	if err != nil {
		t.Fatalf("NewKeychainFromBase64Map: %v", err)
	}
	if kc.ActiveKID() != 0x02 {
		t.Errorf("ActiveKID = 0x%02x, want 0x02", kc.ActiveKID())
	}
}

func TestNewKeychainFromBase64Map_BadBase64(t *testing.T) {
	_, err := NewKeychainFromBase64Map(0x01, map[byte]string{
		0x01: "not valid base64 !!! @#$",
	})
	if err == nil {
		t.Fatal("expected error on garbage base64")
	}
	if codeOf(err) != CodeKeyBase64 {
		t.Errorf("code = %q, want %q", codeOf(err), CodeKeyBase64)
	}
}

func TestKeychain_SealOpen_RoundTrip(t *testing.T) {
	kc, _ := NewKeychain(0x01, map[byte][]byte{
		0x01: mustRand32(t),
	})
	sealed, err := kc.Seal([]byte("hello"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if sealed[0] != keychainVersion {
		t.Errorf("version byte = 0x%02x, want 0x%02x", sealed[0], keychainVersion)
	}
	if sealed[1] != 0x01 {
		t.Errorf("kid byte = 0x%02x, want 0x01", sealed[1])
	}
	got, err := kc.Open(sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("round-trip = %q, want hello", got)
	}
}

func TestKeychain_Rotation_OldBlobOpensAfterActiveChange(t *testing.T) {
	// Phase 1: only kid 0x01 in chain, active = 0x01.
	keyV1 := mustRand32(t)
	kcOld, _ := NewKeychain(0x01, map[byte][]byte{0x01: keyV1})
	sealedV1, _ := kcOld.Seal([]byte("legacy blob"))

	// Phase 2: rotate. New chain holds both keys, active flips to 0x02.
	keyV2 := mustRand32(t)
	kcNew, _ := NewKeychain(0x02, map[byte][]byte{
		0x01: keyV1, // kept for back-decrypt
		0x02: keyV2, // new active
	})

	// Old blob must still open under the new chain.
	got, err := kcNew.Open(sealedV1)
	if err != nil {
		t.Fatalf("old blob failed to open under rotated chain: %v", err)
	}
	if string(got) != "legacy blob" {
		t.Errorf("opened = %q, want 'legacy blob'", got)
	}

	// New seals tag with the new active kid.
	sealedV2, _ := kcNew.Seal([]byte("fresh blob"))
	if sealedV2[1] != 0x02 {
		t.Errorf("fresh-blob kid byte = 0x%02x, want 0x02", sealedV2[1])
	}

	// Phase 3: rotation complete, drop keyV1.
	kcFinal, _ := NewKeychain(0x02, map[byte][]byte{0x02: keyV2})

	// Old blob can no longer open — that's the operational signal to
	// re-seal everything before this phase. Wrong-kid collapses into
	// CodeCiphertext per the wire-leak rule (callers must not branch
	// on the specific failure cause).
	_, missingKidErr := kcFinal.Open(sealedV1)
	if missingKidErr == nil {
		t.Fatal("expected old blob to be undecryptable after keyV1 removal")
	}
	if codeOf(missingKidErr) != CodeCiphertext {
		t.Errorf("expected CodeCiphertext on missing-kid open, got %q", codeOf(missingKidErr))
	}

	// And new blobs continue to open fine.
	got, err = kcFinal.Open(sealedV2)
	if err != nil {
		t.Fatalf("fresh blob failed to open in final phase: %v", err)
	}
	if string(got) != "fresh blob" {
		t.Errorf("opened = %q, want 'fresh blob'", got)
	}
}

func TestKeychain_Open_RejectsMasterKeyBlob(t *testing.T) {
	// Cross-type isolation: a MasterKey blob (version 0x01) must
	// NOT decrypt under a Keychain.Open call (which expects version
	// 0x02). The two types have non-overlapping version domains so
	// callers can't mix them accidentally.
	mk, _ := NewMasterKey(mustRand32(t))
	kc, _ := NewKeychain(0x01, map[byte][]byte{0x01: mustRand32(t)})
	mkBlob, _ := mk.Seal([]byte("hello"))
	if _, err := kc.Open(mkBlob); err == nil {
		t.Fatal("Keychain.Open accepted a MasterKey blob (cross-version leak)")
	}
}

func TestMasterKey_Open_RejectsKeychainBlob(t *testing.T) {
	// Symmetric to above.
	mk, _ := NewMasterKey(mustRand32(t))
	kc, _ := NewKeychain(0x01, map[byte][]byte{0x01: mustRand32(t)})
	kcBlob, _ := kc.Seal([]byte("hello"))
	if _, err := mk.Open(kcBlob); err == nil {
		t.Fatal("MasterKey.Open accepted a Keychain blob (cross-version leak)")
	}
}

func TestKeychain_Seal_NonceFreshPerCall(t *testing.T) {
	// Same nonce-uniqueness guard as the MasterKey version.
	kc, _ := NewKeychain(0x01, map[byte][]byte{0x01: mustRand32(t)})
	a, _ := kc.Seal([]byte("same"))
	b, _ := kc.Seal([]byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two Seal calls of the same plaintext produced identical blobs (nonce reuse!)")
	}
}

func TestKeychain_Open_RejectsUnknownKid(t *testing.T) {
	// Manually craft a blob with version 0x02 + kid 0xff and short
	// random tail. Open must reject without leaking which check
	// failed (kid lookup vs short blob vs tag mismatch — all → CodeCiphertext).
	kc, _ := NewKeychain(0x01, map[byte][]byte{0x01: mustRand32(t)})
	bad := []byte{0x02, 0xff}
	bad = append(bad, bytes.Repeat([]byte{0x00}, 28)...) // header + ciphertext-ish
	_, err := kc.Open(bad)
	if err == nil {
		t.Fatal("Open accepted unknown-kid blob")
	}
	if codeOf(err) != CodeCiphertext {
		t.Errorf("code = %q, want %q", codeOf(err), CodeCiphertext)
	}
}
