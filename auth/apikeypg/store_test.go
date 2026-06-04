package apikeypg_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/apikeypg"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

var (
	pgOnce sync.Once
	pgCfg  db.Config
	pgErr  error
)

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func initContainer() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c, err := tcpg.Run(ctx, "postgres:16-alpine",
		tcpg.WithDatabase("test"), tcpg.WithUsername("test"), tcpg.WithPassword("test"),
		tcpg.BasicWaitStrategies(),
	)
	if err != nil {
		pgErr = err
		return
	}
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "5432/tcp")
	p, _ := strconv.Atoi(port.Port())
	pgCfg = db.Config{
		Host: host, Port: p, User: "test", Password: "test", Database: "test",
		SSLMode: "disable", ConnectTimeout: 5 * time.Second,
		MaxConns: 1, MinConns: 1,
	}
}

func freshDB(t *testing.T) *db.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("requires Postgres testcontainer")
	}
	pgOnce.Do(initContainer)
	if pgErr != nil {
		t.Fatalf("container: %v", pgErr)
	}
	d, err := db.Connect(context.Background(), pgCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	schema := fmt.Sprintf("apk_%d", time.Now().UnixNano())
	if _, err := d.Pool().Exec(context.Background(),
		fmt.Sprintf("CREATE SCHEMA %s; SET search_path TO %s", schema, schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(context.Background(), apikeypg.Schema()); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestInsertAndLookup_HappyPath(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	secret := []byte("kit-secret-32-bytes_____________")
	plain := "ak_abc123"
	hash := auth.HashAPIKey(plain, secret)

	id, err := store.Insert(context.Background(), apikeypg.InsertParams{
		KeyHash: hash, Subject: "svc-a", Scopes: []string{"read", "write"}, Role: "service",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id == "" {
		t.Fatal("Insert returned empty id")
	}

	rec, err := store.Lookup(context.Background(), hash)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if rec.Subject != "svc-a" || rec.Role != "service" {
		t.Errorf("rec = %+v", rec)
	}
	if len(rec.Scopes) != 2 || rec.Scopes[0] != "read" {
		t.Errorf("scopes = %v", rec.Scopes)
	}
	if !rec.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt = %v, want zero (no expiry)", rec.ExpiresAt)
	}
	if !rec.RevokedAt.IsZero() {
		t.Errorf("RevokedAt = %v, want zero (not revoked)", rec.RevokedAt)
	}
}

func TestLookup_NotFound(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	_, err := store.Lookup(context.Background(), []byte("does-not-exist"))
	if err == nil {
		t.Fatal("expected error")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindNotFound {
		t.Fatalf("err = %v, want KindNotFound", err)
	}
}

func TestInsert_WithExpiry_RoundTrips(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	secret := []byte("kit-secret-32-bytes_____________")
	hash := auth.HashAPIKey("k", secret)
	want := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	if _, err := store.Insert(context.Background(), apikeypg.InsertParams{
		KeyHash: hash, Subject: "svc-x", ExpiresAt: want,
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := store.Lookup(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.ExpiresAt.UTC().Truncate(time.Second); !got.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", got, want)
	}
}

func TestRevokeByID_SetsRevokedAt(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	secret := []byte("kit-secret-32-bytes_____________")
	hash := auth.HashAPIKey("k", secret)

	id, err := store.Insert(context.Background(), apikeypg.InsertParams{
		KeyHash: hash, Subject: "svc-x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeByID(context.Background(), id); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	rec, err := store.Lookup(context.Background(), hash)
	if err != nil {
		t.Fatalf("Lookup post-revoke: %v", err)
	}
	if rec.RevokedAt.IsZero() {
		t.Error("RevokedAt should be non-zero after RevokeByID")
	}
}

func TestRevokeByID_NotFound(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	err := store.RevokeByID(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("expected error")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindNotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestInsert_StoresPrefix(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	secret := []byte("kit-secret-32-bytes_____________")
	hash := auth.HashAPIKey("ak_abcd1234", secret)
	id, err := store.Insert(context.Background(), apikeypg.InsertParams{
		KeyHash: hash, Prefix: "ak_abcd", Subject: "svc-p",
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if info.Prefix != "ak_abcd" {
		t.Errorf("prefix = %q, want ak_abcd", info.Prefix)
	}
	if info.Subject != "svc-p" {
		t.Errorf("subject = %q", info.Subject)
	}
}

func TestGet_NotFound(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	_, err := store.Get(context.Background(), "00000000-0000-0000-0000-000000000000")
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindNotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestListBySubject_OrdersByCreatedDesc(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	secret := []byte("kit-secret-32-bytes_____________")
	for i := 0; i < 3; i++ {
		hash := auth.HashAPIKey(fmt.Sprintf("k-%d", i), secret)
		if _, err := store.Insert(context.Background(), apikeypg.InsertParams{
			KeyHash: hash, Subject: "svc-list", Description: fmt.Sprintf("k%d", i),
		}); err != nil {
			t.Fatal(err)
		}
		// Ensure created_at differs per row (timestamptz has microsecond
		// resolution but parallel inserts on a fast machine can collide).
		time.Sleep(2 * time.Millisecond)
	}
	// other subject – must NOT appear.
	if _, err := store.Insert(context.Background(), apikeypg.InsertParams{
		KeyHash: auth.HashAPIKey("other", secret), Subject: "svc-other",
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListBySubject(context.Background(), "svc-list")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if rows[0].Description != "k2" || rows[2].Description != "k0" {
		t.Errorf("order wrong: %v", []string{rows[0].Description, rows[1].Description, rows[2].Description})
	}
}

func TestListBySubject_EmptySubject(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	rows, err := store.ListBySubject(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0 for empty subject", len(rows))
	}
}

func TestRevokeBySubject_BulkRevoke(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	secret := []byte("kit-secret-32-bytes_____________")
	for i := 0; i < 3; i++ {
		_, err := store.Insert(context.Background(), apikeypg.InsertParams{
			KeyHash: auth.HashAPIKey(fmt.Sprintf("svc-bulk-%d", i), secret),
			Subject: "svc-bulk",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Pre-revoke one — RevokeBySubject must only touch active rows.
	rows, _ := store.ListBySubject(context.Background(), "svc-bulk")
	if err := store.RevokeByID(context.Background(), rows[0].ID); err != nil {
		t.Fatal(err)
	}

	n, err := store.RevokeBySubject(context.Background(), "svc-bulk")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("revoked = %d, want 2 (1 pre-revoked, 2 active)", n)
	}
	// Idempotent — second call revokes nothing.
	n2, err := store.RevokeBySubject(context.Background(), "svc-bulk")
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("second call = %d, want 0", n2)
	}
	// Unknown subject is not an error.
	n3, err := store.RevokeBySubject(context.Background(), "nobody")
	if err != nil {
		t.Fatal(err)
	}
	if n3 != 0 {
		t.Errorf("unknown subject = %d, want 0", n3)
	}
}

func TestStats_ActiveExpiredRevokedTotal(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	secret := []byte("kit-secret-32-bytes_____________")

	insert := func(label string, expires time.Time) string {
		id, err := store.Insert(context.Background(), apikeypg.InsertParams{
			KeyHash: auth.HashAPIKey(label, secret), Subject: "svc-s",
			ExpiresAt: expires,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	_ = insert("active1", time.Time{})
	_ = insert("active2", time.Now().Add(1*time.Hour))
	_ = insert("expired1", time.Now().Add(-1*time.Hour))
	revokedID := insert("revoked1", time.Time{})
	if err := store.RevokeByID(context.Background(), revokedID); err != nil {
		t.Fatal(err)
	}

	s, err := store.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Active != 2 || s.Expired != 1 || s.Revoked != 1 || s.Total != 4 {
		t.Errorf("stats = %+v, want active=2 expired=1 revoked=1 total=4", s)
	}
}

func TestDeleteExpired_OnlyEligibleRows(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	secret := []byte("kit-secret-32-bytes_____________")
	ctx := context.Background()

	// active, no expiry — never deleted.
	_, _ = store.Insert(ctx, apikeypg.InsertParams{
		KeyHash: auth.HashAPIKey("active", secret), Subject: "svc-d",
	})
	// active, expiring in future — never deleted.
	_, _ = store.Insert(ctx, apikeypg.InsertParams{
		KeyHash:   auth.HashAPIKey("future", secret),
		Subject:   "svc-d",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})
	// expired long ago — eligible.
	_, _ = store.Insert(ctx, apikeypg.InsertParams{
		KeyHash:   auth.HashAPIKey("expired", secret),
		Subject:   "svc-d",
		ExpiresAt: time.Now().Add(-72 * time.Hour),
	})
	// recently revoked — not eligible if cutoff < revoked_at.
	recentID, _ := store.Insert(ctx, apikeypg.InsertParams{
		KeyHash: auth.HashAPIKey("recent-revoke", secret), Subject: "svc-d",
	})
	_ = store.RevokeByID(ctx, recentID)

	cutoff := time.Now().Add(-24 * time.Hour)
	n, err := store.DeleteExpired(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("deleted = %d, want 1 (only the long-expired row)", n)
	}
	rows, _ := store.ListBySubject(ctx, "svc-d")
	if len(rows) != 3 {
		t.Errorf("remaining rows = %d, want 3", len(rows))
	}
}

func TestRotate_HappyPath(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	secret := []byte("kit-secret-32-bytes_____________")
	oldHash := auth.HashAPIKey("old", secret)
	id, err := store.Insert(context.Background(), apikeypg.InsertParams{
		KeyHash: oldHash, Prefix: "ak_old", Subject: "svc-r",
	})
	if err != nil {
		t.Fatal(err)
	}
	newHash := auth.HashAPIKey("new", secret)
	if err := store.Rotate(context.Background(), id, newHash, "ak_new"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	// Old hash no longer hits.
	if _, err := store.Lookup(context.Background(), oldHash); err == nil {
		t.Error("old hash still resolves after Rotate")
	}
	// New hash resolves to the same id.
	rec, err := store.Lookup(context.Background(), newHash)
	if err != nil {
		t.Fatalf("Lookup new: %v", err)
	}
	if rec.ID != id {
		t.Errorf("id changed: %q != %q", rec.ID, id)
	}
	info, _ := store.Get(context.Background(), id)
	if info.Prefix != "ak_new" {
		t.Errorf("prefix not updated: %q", info.Prefix)
	}
}

func TestRotate_NotFoundOnRevoked(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	secret := []byte("kit-secret-32-bytes_____________")
	id, err := store.Insert(context.Background(), apikeypg.InsertParams{
		KeyHash: auth.HashAPIKey("z", secret), Subject: "svc-z",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.RevokeByID(context.Background(), id)

	newHash := auth.HashAPIKey("z2", secret)
	err = store.Rotate(context.Background(), id, newHash, "")
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindNotFound {
		t.Errorf("err = %v, want NotFound on revoked key", err)
	}
}

func TestRotate_RejectsEmptyHash(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	err := store.Rotate(context.Background(), "00000000-0000-0000-0000-000000000000", nil, "")
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		t.Errorf("err = %v, want KindValidation", err)
	}
}

func TestUpdateScopes_ReplacesScopes(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	secret := []byte("kit-secret-32-bytes_____________")
	id, _ := store.Insert(context.Background(), apikeypg.InsertParams{
		KeyHash: auth.HashAPIKey("u", secret), Subject: "svc-u",
		Scopes: []string{"read"},
	})
	if err := store.UpdateScopes(context.Background(), id, []string{"read", "write"}); err != nil {
		t.Fatalf("UpdateScopes: %v", err)
	}
	info, _ := store.Get(context.Background(), id)
	if len(info.Scopes) != 2 || info.Scopes[1] != "write" {
		t.Errorf("scopes = %v, want [read write]", info.Scopes)
	}
	// nil coerces to '{}' — no error.
	if err := store.UpdateScopes(context.Background(), id, nil); err != nil {
		t.Fatalf("UpdateScopes(nil): %v", err)
	}
	info, _ = store.Get(context.Background(), id)
	if len(info.Scopes) != 0 {
		t.Errorf("scopes after nil = %v, want []", info.Scopes)
	}
}

func TestUpdateScopes_NotFoundOnRevoked(t *testing.T) {
	d := freshDB(t)
	store := apikeypg.New(d)
	secret := []byte("kit-secret-32-bytes_____________")
	id, _ := store.Insert(context.Background(), apikeypg.InsertParams{
		KeyHash: auth.HashAPIKey("u2", secret), Subject: "svc-u",
	})
	_ = store.RevokeByID(context.Background(), id)
	err := store.UpdateScopes(context.Background(), id, []string{"x"})
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindNotFound {
		t.Errorf("err = %v, want NotFound", err)
	}
}
