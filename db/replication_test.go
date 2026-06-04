package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stubRow lets us return a fake QueryRow result for queryReplicaLag.
type stubRow struct {
	scan func(dest ...any) error
}

func (s stubRow) Scan(dest ...any) error { return s.scan(dest...) }

// stubReader implements poolReader.
type stubReader struct {
	row pgx.Row
}

func (s stubReader) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row { return s.row }

func TestQueryReplicaLag_Healthy(t *testing.T) {
	want := 1.25
	reader := stubReader{row: stubRow{scan: func(dest ...any) error {
		// Scan into **float64 (ptr to *float64).
		p, ok := dest[0].(**float64)
		if !ok {
			t.Fatalf("unexpected dest type %T", dest[0])
		}
		*p = &want
		return nil
	}}}
	info := queryReplicaLag(context.Background(), "standby", reader)
	if !info.Healthy {
		t.Fatalf("Healthy = false, want true; err=%v", info.Err)
	}
	if info.LagSeconds != want {
		t.Errorf("LagSeconds = %v, want %v", info.LagSeconds, want)
	}
	if info.PoolName != "standby" {
		t.Errorf("PoolName = %q, want standby", info.PoolName)
	}
}

func TestQueryReplicaLag_PrimaryReturnsNil(t *testing.T) {
	reader := stubReader{row: stubRow{scan: func(dest ...any) error {
		// Leave *p unset → nil pointer (primary node).
		return nil
	}}}
	info := queryReplicaLag(context.Background(), "standby-1", reader)
	if !info.Healthy {
		t.Errorf("Healthy = false, want true (nil = lag 0 on primary)")
	}
	if info.LagSeconds != 0 {
		t.Errorf("LagSeconds = %v, want 0", info.LagSeconds)
	}
}

func TestQueryReplicaLag_FailureSetsErr(t *testing.T) {
	want := errors.New("network down")
	reader := stubReader{row: stubRow{scan: func(dest ...any) error { return want }}}
	info := queryReplicaLag(context.Background(), "standby-3", reader)
	if info.Healthy {
		t.Errorf("Healthy = true, want false")
	}
	if !errors.Is(info.Err, want) {
		t.Errorf("Err = %v, want network down", info.Err)
	}
	if info.LagSeconds != 0 {
		t.Errorf("LagSeconds = %v, want 0 on failure", info.LagSeconds)
	}
}

func TestParseReadRoute(t *testing.T) {
	cases := []struct {
		in      string
		want    readRoute
		wantErr bool
	}{
		{"", routeRoundRobin, false},
		{"round_robin", routeRoundRobin, false},
		{"Round-Robin", routeRoundRobin, false},
		{"roundrobin", routeRoundRobin, false},
		{"random", routeRandom, false},
		{"RANDOM", routeRandom, false},
		{"bogus", 0, true},
	}
	for _, tc := range cases {
		got, err := parseReadRoute(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseReadRoute(%q) err = nil, want error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseReadRoute(%q) err = %v, want nil", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseReadRoute(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestNonEmptyStrings(t *testing.T) {
	got := nonEmptyStrings([]string{"a", "", " ", " b ", "c"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNormalizeReadURL_InjectsStandbyAttr(t *testing.T) {
	url, err := normalizeReadURL("postgres://u:p@h:5432/db?sslmode=disable")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !strings.Contains(url, "target_session_attrs=standby") {
		t.Errorf("missing target_session_attrs=standby: %s", url)
	}
}

func TestNormalizeReadURL_PreservesExplicitAttr(t *testing.T) {
	url, err := normalizeReadURL("postgres://u:p@h:5432/db?target_session_attrs=any")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !strings.Contains(url, "target_session_attrs=any") {
		t.Errorf("explicit target_session_attrs=any was clobbered: %s", url)
	}
	if strings.Contains(url, "target_session_attrs=standby") {
		t.Errorf("standby was injected over explicit any: %s", url)
	}
}

func TestReadFromPrimary_MarksContext(t *testing.T) {
	ctx := context.Background()
	if readFromPrimaryRequested(ctx) {
		t.Error("unmarked ctx should not request primary")
	}
	marked := ReadFromPrimary(ctx)
	if !readFromPrimaryRequested(marked) {
		t.Error("marked ctx should request primary")
	}
	// Original ctx untouched (immutable contract).
	if readFromPrimaryRequested(ctx) {
		t.Error("ReadFromPrimary should not mutate the parent ctx")
	}
}

func TestPickReadPool_NoReplicasFallsBackToPrimary(t *testing.T) {
	primary := &pgxpool.Pool{} // pointer-identity only; we don't dereference
	d := &DB{pool: primary}
	if got := d.pickReadPool(context.Background()); got != primary {
		t.Errorf("expected primary fallback, got %p (want %p)", got, primary)
	}
}

func TestPickReadPool_ReadFromPrimaryForcesPrimary(t *testing.T) {
	primary := &pgxpool.Pool{}
	rp := &pgxpool.Pool{}
	d := &DB{
		pool:      primary,
		readPools: []readPoolEntry{{name: "standby-1", pool: rp}},
	}
	ctx := ReadFromPrimary(context.Background())
	if got := d.pickReadPool(ctx); got != primary {
		t.Errorf("ReadFromPrimary did not force primary; got %p, want %p", got, primary)
	}
}

func TestPickReadPool_RoundRobin(t *testing.T) {
	a, b, c := &pgxpool.Pool{}, &pgxpool.Pool{}, &pgxpool.Pool{}
	d := &DB{
		pool:  &pgxpool.Pool{},
		route: routeRoundRobin,
		readPools: []readPoolEntry{
			{name: "1", pool: a}, {name: "2", pool: b}, {name: "3", pool: c},
		},
	}
	ctx := context.Background()
	got := []*pgxpool.Pool{
		d.pickReadPool(ctx),
		d.pickReadPool(ctx),
		d.pickReadPool(ctx),
		d.pickReadPool(ctx),
	}
	// nextRead starts at 0, so the sequence is a,b,c,a.
	want := []*pgxpool.Pool{a, b, c, a}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("step %d: got %p, want %p", i, got[i], want[i])
		}
	}
}

func TestPickReadPool_SingleReplicaSkipsRouting(t *testing.T) {
	rp := &pgxpool.Pool{}
	d := &DB{
		pool:      &pgxpool.Pool{},
		route:     routeRandom, // shouldn't fire rand.IntN with len 1
		readPools: []readPoolEntry{{name: "only", pool: rp}},
	}
	for i := 0; i < 10; i++ {
		if got := d.pickReadPool(context.Background()); got != rp {
			t.Fatalf("single-replica path returned %p, want %p", got, rp)
		}
	}
}

func TestReplicationLag_NoReplicasReturnsEmpty(t *testing.T) {
	d := &DB{pool: &pgxpool.Pool{}}
	if got := d.ReplicationLag(context.Background()); len(got) != 0 {
		t.Errorf("no-replica DB.ReplicationLag = %v, want empty", got)
	}
}
