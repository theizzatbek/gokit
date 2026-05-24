package auth

import (
	"context"
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
	if len(raw) < 40 { // "rt_" + base64url(32B) ≈ 47
		t.Errorf("raw too short: %d bytes", len(raw))
	}
	if got := hashRefresh(raw); got != hash {
		t.Errorf("hashRefresh(raw) != hash returned from newRawRefresh")
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
