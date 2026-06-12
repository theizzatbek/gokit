package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"

	xerrs "github.com/theizzatbek/gokit/errs"
)

func mustPepper(t *testing.T) []byte {
	t.Helper()
	pepper := make([]byte, 32)
	if _, err := rand.Read(pepper); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return pepper
}

func TestGenerateAPIKey_HappyPath(t *testing.T) {
	pepper := mustPepper(t)
	plain, hash, prefix, err := GenerateAPIKey(pepper)
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}

	// Plain shape.
	if !strings.HasPrefix(plain, APIKeyPrefix) {
		t.Errorf("plain %q missing %q prefix", plain, APIKeyPrefix)
	}
	wantLen := len(APIKeyPrefix) + 28 // "ak_" + 28 base64-RawURL chars
	if len(plain) != wantLen {
		t.Errorf("plain length = %d, want %d", len(plain), wantLen)
	}

	// Hash shape.
	if len(hash) != 32 {
		t.Errorf("hash length = %d, want 32 (HMAC-SHA256)", len(hash))
	}

	// Prefix shape.
	if len(prefix) != apiKeyPrefixLen {
		t.Errorf("prefix length = %d, want %d", len(prefix), apiKeyPrefixLen)
	}
	if prefix != plain[:apiKeyPrefixLen] {
		t.Errorf("prefix %q != plain[:%d] = %q", prefix, apiKeyPrefixLen, plain[:apiKeyPrefixLen])
	}
	if !strings.HasPrefix(prefix, APIKeyPrefix) {
		t.Errorf("prefix %q doesn't start with %q", prefix, APIKeyPrefix)
	}
}

func TestGenerateAPIKey_TailIsBase64RawURL(t *testing.T) {
	plain, _, _, err := GenerateAPIKey(mustPepper(t))
	if err != nil {
		t.Fatal(err)
	}
	tail := plain[len(APIKeyPrefix):]
	// Base64 RawURL has the alphabet [A-Za-z0-9_-]. No padding.
	if strings.Contains(tail, "=") {
		t.Errorf("tail %q contains padding character — RawURL must be unpadded", tail)
	}
	// Round-trip via RawURLEncoding.
	raw, err := base64.RawURLEncoding.DecodeString(tail)
	if err != nil {
		t.Errorf("tail %q failed RawURL decode: %v", tail, err)
	}
	if len(raw) != apiKeyRandBytes {
		t.Errorf("decoded length = %d, want %d", len(raw), apiKeyRandBytes)
	}
}

func TestGenerateAPIKey_HashMatchesHashAPIKey(t *testing.T) {
	// The whole point of GenerateAPIKey is producing (plain, hash)
	// where HashAPIKey(plain, pepper) equals the returned hash. If
	// these drift, every key issued via GenerateAPIKey fails to
	// authenticate at login time.
	pepper := mustPepper(t)
	plain, hash, _, err := GenerateAPIKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	want := HashAPIKey(plain, pepper)
	if !hmac.Equal(hash, want) {
		t.Errorf("GenerateAPIKey hash != HashAPIKey(plain, pepper)\n  got:  %x\n  want: %x", hash, want)
	}
}

func TestGenerateAPIKey_HashRequiresMatchingPepper(t *testing.T) {
	// Issuing under pepperA and verifying under pepperB MUST produce
	// non-equal hashes — that's the whole HMAC-pepper-rotation
	// invariant. Without it, rotating Config.APIKeyHashSecret wouldn't
	// actually invalidate the old key set.
	pepperA := mustPepper(t)
	pepperB := mustPepper(t)
	plain, hashA, _, err := GenerateAPIKey(pepperA)
	if err != nil {
		t.Fatal(err)
	}
	hashB := HashAPIKey(plain, pepperB)
	if hmac.Equal(hashA, hashB) {
		t.Error("same plain under two distinct peppers produced equal hashes (pepper not honoured)")
	}
}

func TestGenerateAPIKey_PlainIsUnique(t *testing.T) {
	pepper := mustPepper(t)
	const N = 1000
	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		plain, _, _, err := GenerateAPIKey(pepper)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if _, dup := seen[plain]; dup {
			t.Fatalf("duplicate plain in %d calls: %q", i, plain)
		}
		seen[plain] = struct{}{}
	}
}

func TestGenerateAPIKey_GoroutineSafe(t *testing.T) {
	// crypto/rand.Reader is safe under concurrent use per stdlib
	// contract; HashAPIKey is pure. This test guards against any
	// future refactor that adds shared state to GenerateAPIKey
	// without locking.
	pepper := mustPepper(t)
	const workers = 8
	const each = 500
	var (
		mu   sync.Mutex
		seen = make(map[string]struct{}, workers*each)
		errs sync.Map
	)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < each; j++ {
				plain, _, _, err := GenerateAPIKey(pepper)
				if err != nil {
					errs.Store(i*each+j, err)
					return
				}
				mu.Lock()
				_, dup := seen[plain]
				seen[plain] = struct{}{}
				mu.Unlock()
				if dup {
					errs.Store(i*each+j, "duplicate plain across goroutines")
					return
				}
			}
		}(i)
	}
	wg.Wait()
	errs.Range(func(k, v any) bool {
		t.Errorf("worker iter %v: %v", k, v)
		return true
	})
	if got := len(seen); got != workers*each {
		t.Errorf("collected %d unique plains, want %d", got, workers*each)
	}
}

func TestGenerateAPIKey_RejectsShortPepper(t *testing.T) {
	cases := []int{0, 1, 16, 24, 31}
	for _, n := range cases {
		pepper := make([]byte, n)
		_, _, _, err := GenerateAPIKey(pepper)
		if err == nil {
			t.Errorf("GenerateAPIKey(pepper=%d bytes) err = nil, want CodeKeygenBadPepper", n)
			continue
		}
		var e *xerrs.Error
		if !errors.As(err, &e) {
			t.Errorf("GenerateAPIKey(pepper=%d) not a *errs.Error: %v", n, err)
			continue
		}
		if e.Code != CodeKeygenBadPepper {
			t.Errorf("GenerateAPIKey(pepper=%d) code = %q, want %q", n, e.Code, CodeKeygenBadPepper)
		}
	}
}

func TestGenerateAPIKey_AcceptsExactFloorPepper(t *testing.T) {
	// 32 bytes is the floor — exactly at the boundary must succeed.
	pepper := make([]byte, 32)
	if _, err := rand.Read(pepper); err != nil {
		t.Fatal(err)
	}
	plain, hash, prefix, err := GenerateAPIKey(pepper)
	if err != nil {
		t.Fatalf("exactly-32 pepper rejected: %v", err)
	}
	if !strings.HasPrefix(plain, APIKeyPrefix) || len(hash) != 32 || len(prefix) != apiKeyPrefixLen {
		t.Errorf("output shape wrong at boundary: plain=%q hash=%d prefix=%q",
			plain, len(hash), prefix)
	}
}

func TestGenerateAPIKey_PrefixDoesntLeakEntireKey(t *testing.T) {
	// Defence-in-depth: prefix is the first 8 chars; the remaining
	// 23 chars of plain MUST NOT be recoverable from prefix alone.
	// This is a structural assertion — if prefix == plain then a
	// future refactor would have broken the safe-to-display
	// contract.
	plain, _, prefix, err := GenerateAPIKey(mustPepper(t))
	if err != nil {
		t.Fatal(err)
	}
	if prefix == plain {
		t.Fatal("prefix equals plain — safe-to-display contract broken")
	}
	if len(plain)-len(prefix) < 16 {
		t.Errorf("prefix exposes too much of plain: prefix=%d plain=%d (delta=%d, want ≥ 16)",
			len(prefix), len(plain), len(plain)-len(prefix))
	}
	if !bytes.HasPrefix([]byte(plain), []byte(prefix)) {
		t.Error("plain doesn't start with prefix — invariant broken")
	}
}
