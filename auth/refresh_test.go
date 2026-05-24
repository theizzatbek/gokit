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
}

// Compile-time assertion: any future store fake must satisfy RefreshStore.
var _ RefreshStore = (*nopStore)(nil)

type nopStore struct{}

func (nopStore) Issue(_ context.Context, _ Record) error { return nil }
func (nopStore) Consume(_ context.Context, _ [32]byte, _ time.Time) (Record, error) {
	return Record{}, nil
}
func (nopStore) RevokeFamily(_ context.Context, _ string) error               { return nil }
func (nopStore) RevokeSubject(_ context.Context, _ string) error              { return nil }
func (nopStore) GarbageCollect(_ context.Context, _ time.Time) (int64, error) { return 0, nil }
