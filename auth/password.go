package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	xerrs "github.com/theizzatbek/fibermap/errs"
)

// Params controls argon2id cost. DefaultParams returns OWASP 2024 values for
// interactive logins. Tuning upward is safe: PHC-encoded hashes carry their
// own parameters, so old hashes still verify with their original cost.
type Params struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLen     uint32
	KeyLen      uint32
}

// DefaultParams returns a fresh copy of the recommended defaults.
func DefaultParams() Params {
	return Params{
		Memory:      19 * 1024,
		Iterations:  2,
		Parallelism: 1,
		SaltLen:     16,
		KeyLen:      32,
	}
}

// Hasher computes and verifies argon2id PHC-encoded hashes.
type Hasher struct{ params Params }

// NewHasher returns a Hasher with the given params. Use DefaultHasher when no
// tuning is required.
func NewHasher(p Params) *Hasher { return &Hasher{params: p} }

// DefaultHasher is a process-wide hasher with OWASP-2024 defaults.
var DefaultHasher = NewHasher(DefaultParams())

// Hash returns a PHC-encoded argon2id hash:
//
//	$argon2id$v=19$m=<KiB>,t=<iter>,p=<para>$<b64salt>$<b64hash>
func (h *Hasher) Hash(password string) (string, error) {
	if err := h.validateParams(); err != nil {
		return "", err
	}
	salt := make([]byte, h.params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", xerrs.Wrap(err, xerrs.KindInternal, "rand_failed", "read salt")
	}
	key := argon2.IDKey([]byte(password), salt,
		h.params.Iterations, h.params.Memory, h.params.Parallelism, h.params.KeyLen)
	enc := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.params.Memory, h.params.Iterations, h.params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
	return enc, nil
}

// Verify compares password against an encoded hash. nil = match;
// *errs.Error{KindUnauthorized,CodeInvalidCredentials} = mismatch;
// *errs.Error{KindInternal,CodePasswordHashCorrupt} = malformed encoded.
func (h *Hasher) Verify(encoded, password string) error {
	p, salt, key, err := decodePHC(encoded)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodePasswordHashCorrupt, "corrupt password hash")
	}
	cmp := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(key)))
	if subtle.ConstantTimeCompare(cmp, key) != 1 {
		return xerrs.Unauthorized(CodeInvalidCredentials, "invalid login or password")
	}
	return nil
}

// validateParams returns nil if the Hasher's Params are usable, otherwise a
// typed *errs.Error{KindInternal, "invalid_params"}. Catches the common misuse
// of building a Hasher from a partially-populated Params struct.
func (h *Hasher) validateParams() error {
	switch {
	case h.params.KeyLen < 4:
		return xerrs.Internalf("invalid_params", "Hasher.params.KeyLen=%d, must be >= 4", h.params.KeyLen)
	case h.params.SaltLen < 8:
		return xerrs.Internalf("invalid_params", "Hasher.params.SaltLen=%d, must be >= 8", h.params.SaltLen)
	case h.params.Memory < 8:
		return xerrs.Internalf("invalid_params", "Hasher.params.Memory=%d KiB, must be >= 8", h.params.Memory)
	case h.params.Iterations < 1:
		return xerrs.Internalf("invalid_params", "Hasher.params.Iterations=%d, must be >= 1", h.params.Iterations)
	case h.params.Parallelism < 1:
		return xerrs.Internalf("invalid_params", "Hasher.params.Parallelism=%d, must be >= 1", h.params.Parallelism)
	}
	return nil
}

// NeedsRehash returns true iff encoded was produced with weaker cost than the
// Hasher's current params. False on corrupt encoded — callers must NOT loop
// rehash attempts on broken inputs.
func (h *Hasher) NeedsRehash(encoded string) bool {
	p, _, _, err := decodePHC(encoded)
	if err != nil {
		return false
	}
	if p.Memory < h.params.Memory {
		return true
	}
	if p.Iterations < h.params.Iterations {
		return true
	}
	if p.Parallelism < h.params.Parallelism {
		return true
	}
	if p.KeyLen < h.params.KeyLen {
		return true
	}
	return false
}

func decodePHC(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// Expected shape: ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<key>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return Params{}, nil, nil, errors.New("not argon2id PHC")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Params{}, nil, nil, fmt.Errorf("unsupported argon2 version: %q", parts[2])
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return Params{}, nil, nil, fmt.Errorf("bad params segment: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("bad salt b64: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("bad hash b64: %w", err)
	}
	// RFC 9106 §3.1: salt MUST be >= 8 bytes, tag (key) MUST be >= 4 bytes.
	// Anything shorter is malformed — treat as corrupt rather than silently
	// running argon2id with degenerate inputs and surfacing as "wrong password".
	if len(salt) < 8 {
		return Params{}, nil, nil, fmt.Errorf("salt too short: %d bytes", len(salt))
	}
	if len(key) < 4 {
		return Params{}, nil, nil, fmt.Errorf("key too short: %d bytes", len(key))
	}
	p.SaltLen = uint32(len(salt))
	p.KeyLen = uint32(len(key))
	return p, salt, key, nil
}
