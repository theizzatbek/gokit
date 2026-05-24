package auth

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

func TestNewRawRefresh_PrefixAndLength(t *testing.T) {
	raw, hash, err := newRawRefresh()
	if err != nil {
		t.Fatalf("newRawRefresh: %v", err)
	}
	if !strings.HasPrefix(raw, "rt_") {
		t.Errorf("raw missing rt_ prefix: %q", raw)
	}
	if len(raw) != 46 { // "rt_" (3) + base64.RawURLEncoding.EncodedLen(32) (43)
		t.Errorf("raw length = %d, want 46", len(raw))
	}
	if got := hashRefresh(raw); got != hash {
		t.Errorf("hashRefresh(raw) != hash returned from newRawRefresh")
	}
	want := sha256.Sum256([]byte(raw))
	if hash != want {
		t.Errorf("hash != sha256.Sum256(raw)")
	}
}

func TestNewRawRefresh_NotPredictable(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		raw, _, _ := newRawRefresh()
		if _, dup := seen[raw]; dup {
			t.Fatalf("collision in 100 draws — RNG broken?")
		}
		seen[raw] = struct{}{}
	}
}

func TestHashRefresh_DeterministicAndFixedSize(t *testing.T) {
	a := hashRefresh("rt_xyz")
	b := hashRefresh("rt_xyz")
	if a != b {
		t.Fatalf("hashRefresh not deterministic")
	}
	if len(a) != 32 {
		t.Fatalf("hash length = %d, want 32", len(a))
	}
}

// Compile-time assertion: any future store fake must satisfy RefreshStore.
var _ RefreshStore = (*nopStore)(nil)

type nopStore struct{}

func (nopStore) Issue(ctx context.Context, r Record) error { return nil }
func (nopStore) Consume(ctx context.Context, h [32]byte, now time.Time) (Record, error) {
	return Record{}, nil
}
func (nopStore) RevokeFamily(ctx context.Context, fid string) error               { return nil }
func (nopStore) RevokeSubject(ctx context.Context, s string) error                { return nil }
func (nopStore) GarbageCollect(ctx context.Context, now time.Time) (int64, error) { return 0, nil }
