package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/theizzatbek/gokit/errs"
)

func TestHash_RoundTrip(t *testing.T) {
	h := NewHasher(DefaultParams())
	enc, err := h.Hash("hunter2")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$") {
		t.Fatalf("encoded not PHC argon2id: %q", enc)
	}
	if err := h.Verify(enc, "hunter2"); err != nil {
		t.Fatalf("verify good password: %v", err)
	}
}

func TestVerify_WrongPasswordIsUnauthorized(t *testing.T) {
	h := NewHasher(DefaultParams())
	enc, _ := h.Hash("hunter2")
	err := h.Verify(enc, "wrong")
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindUnauthorized || e.Code != CodeInvalidCredentials {
		t.Fatalf("verify wrong: err = %v", err)
	}
}

func TestVerify_CorruptEncodedIsInternal(t *testing.T) {
	h := NewHasher(DefaultParams())
	for _, bad := range []string{
		"",
		"$argon2id$v=19$broken",
		"$argon2id$v=19$m=1,t=1,p=1$XYZ$ABC",
		"$bcrypt$something",
	} {
		err := h.Verify(bad, "x")
		var e *errs.Error
		if !errors.As(err, &e) || e.Kind != errs.KindInternal || e.Code != CodePasswordHashCorrupt {
			t.Fatalf("expected hash_corrupt for %q, got %v", bad, err)
		}
	}
}

func TestDefaultHasher_HasOWASPDefaults(t *testing.T) {
	p := DefaultParams()
	if p.Memory != 19*1024 {
		t.Errorf("Memory = %d, want %d", p.Memory, 19*1024)
	}
	if p.Iterations != 2 || p.Parallelism != 1 {
		t.Errorf("Iter/Para = %d/%d, want 2/1", p.Iterations, p.Parallelism)
	}
	if p.SaltLen != 16 || p.KeyLen != 32 {
		t.Errorf("Salt/Key len = %d/%d, want 16/32", p.SaltLen, p.KeyLen)
	}
}

func TestHash_TwoCallsProduceDifferentSalts(t *testing.T) {
	h := NewHasher(DefaultParams())
	a, _ := h.Hash("x")
	b, _ := h.Hash("x")
	if a == b {
		t.Fatalf("two hashes equal — salt not randomised")
	}
}

func TestHash_ZeroParamsReturnsError(t *testing.T) {
	h := NewHasher(Params{}) // zero-valued
	_, err := h.Hash("x")
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindInternal || e.Code != "invalid_params" {
		t.Fatalf("expected invalid_params, got %v", err)
	}
}

func TestNeedsRehash_FalseWhenParamsMatch(t *testing.T) {
	h := NewHasher(DefaultParams())
	enc, _ := h.Hash("x")
	if h.NeedsRehash(enc) {
		t.Fatalf("same-params hash flagged as needing rehash")
	}
}

func TestNeedsRehash_TrueWhenStoredParamsAreWeaker(t *testing.T) {
	weak := NewHasher(Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32})
	strong := NewHasher(Params{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32})
	enc, _ := weak.Hash("x")
	if !strong.NeedsRehash(enc) {
		t.Fatalf("weaker hash should need rehash under stronger hasher")
	}
}

func TestNeedsRehash_FalseOnCorruptInput(t *testing.T) {
	if NewHasher(DefaultParams()).NeedsRehash("garbage") {
		t.Fatalf("corrupt encoded must not trigger rehash — caller would loop forever")
	}
}
