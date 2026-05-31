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
