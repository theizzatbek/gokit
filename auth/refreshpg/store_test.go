package refreshpg

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

//go:embed schema.sql
var schemaSQL string

var testDB *db.DB

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

// runMain wraps the TestMain body so defers fire before os.Exit.
func runMain(m *testing.M) int {
	// Parse flags so testing.Short() returns the correct value before we boot
	// the (expensive) container.
	flag.Parse()
	if testing.Short() {
		// Skip the entire package under -short — every test needs
		// the Postgres container, so running them without setup
		// would just nil-deref testDB.
		return 0
	}
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("authtest"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		println("testcontainers postgres start failed:", err.Error())
		return 1
	}
	defer func() {
		if termErr := testcontainers.TerminateContainer(c); termErr != nil {
			println("testcontainers terminate:", termErr.Error())
		}
	}()

	connStr, _ := c.ConnectionString(ctx, "sslmode=disable")
	cfg, err := parseConn(connStr)
	if err != nil {
		println("parse conn:", err.Error())
		return 1
	}
	d, err := db.Connect(ctx, cfg)
	if err != nil {
		println("db.Connect:", err.Error())
		return 1
	}
	defer d.Close()
	if _, err := d.Exec(ctx, schemaSQL); err != nil {
		println("schema:", err.Error())
		return 1
	}
	testDB = d
	return m.Run()
}

func parseConn(connStr string) (db.Config, error) {
	u, err := url.Parse(connStr)
	if err != nil {
		return db.Config{}, err
	}
	port := 5432
	if u.Port() != "" {
		fmt.Sscanf(u.Port(), "%d", &port)
	}
	pass, _ := u.User.Password()
	return db.Config{
		Host:     u.Hostname(),
		Port:     port,
		User:     u.User.Username(),
		Password: pass,
		Database: strings.TrimPrefix(u.Path, "/"),
		SSLMode:  u.Query().Get("sslmode"),
	}, nil
}

func TestIssueRoundTrip(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	s := New(testDB)
	now := time.Now().UTC().Truncate(time.Second)
	var h [32]byte
	h[0] = 0xAB
	rec := auth.Record{
		TokenHash: h, Subject: "u-1", FamilyID: "00000000-0000-0000-0000-000000000001",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), UserAgent: "ua", IP: "127.0.0.1",
	}
	if err := s.Issue(ctx, rec); err != nil {
		t.Fatalf("issue: %v", err)
	}
	var subject string
	if err := testDB.QueryRow(ctx, "SELECT subject FROM auth_refresh_tokens WHERE token_hash = $1", h[:]).Scan(&subject); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if subject != "u-1" {
		t.Fatalf("subject = %q", subject)
	}
}

func TestIssue_DuplicateHashIsConflict(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	s := New(testDB)
	now := time.Now().UTC()
	var h [32]byte
	h[0] = 1
	rec := auth.Record{TokenHash: h, FamilyID: "00000000-0000-0000-0000-000000000002", Subject: "u", IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.Issue(ctx, rec); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := s.Issue(ctx, rec)
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("second issue err type = %T", err)
	}
	if e.Kind != errs.KindAlreadyExists {
		t.Fatalf("second issue Kind = %v, want AlreadyExists", e.Kind)
	}
}

func TestConsume_RoundTrip(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration")
	}
	ctx := context.Background()
	_, _ = testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens")
	s := New(testDB)
	now := time.Now().UTC().Truncate(time.Second)
	var h [32]byte
	h[0] = 1
	_ = s.Issue(ctx, auth.Record{
		TokenHash: h, Subject: "u-1", FamilyID: "11111111-1111-1111-1111-111111111111",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	got, err := s.Consume(ctx, h, now)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got.Subject != "u-1" {
		t.Fatalf("subject = %q", got.Subject)
	}
}

func TestConsume_NotFound(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration")
	}
	ctx := context.Background()
	_, _ = testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens")
	_, err := New(testDB).Consume(ctx, [32]byte{0xFF}, time.Now())
	assertCode(t, err, auth.CodeRefreshInvalid)
}

func TestConsume_Expired(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration")
	}
	ctx := context.Background()
	_, _ = testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens")
	s := New(testDB)
	now := time.Now().UTC()
	var h [32]byte
	h[0] = 1
	_ = s.Issue(ctx, auth.Record{
		TokenHash: h, Subject: "u-1", FamilyID: "22222222-2222-2222-2222-222222222222",
		IssuedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	})
	_, err := s.Consume(ctx, h, now)
	assertCode(t, err, auth.CodeRefreshExpired)
}

func TestConsume_ReusedRevokesFamily(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration")
	}
	ctx := context.Background()
	_, _ = testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens")
	s := New(testDB)
	now := time.Now().UTC()
	fam := "33333333-3333-3333-3333-333333333333"
	var h1, h2 [32]byte
	h1[0] = 1
	h2[0] = 2
	_ = s.Issue(ctx, auth.Record{TokenHash: h1, FamilyID: fam, Subject: "u", IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	_ = s.Issue(ctx, auth.Record{TokenHash: h2, FamilyID: fam, Subject: "u", IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	if _, err := s.Consume(ctx, h1, now); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	_, err := s.Consume(ctx, h1, now)
	assertCode(t, err, auth.CodeRefreshReused)
	// h2 should now be revoked too — Consume on it returns reused.
	_, err2 := s.Consume(ctx, h2, now)
	assertCode(t, err2, auth.CodeRefreshReused)
}

func TestRevokeFamily(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration")
	}
	ctx := context.Background()
	_, _ = testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens")
	s := New(testDB)
	now := time.Now().UTC()
	fam := "44444444-4444-4444-4444-444444444444"
	var h [32]byte
	h[0] = 1
	_ = s.Issue(ctx, auth.Record{TokenHash: h, FamilyID: fam, Subject: "u", IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err := s.RevokeFamily(ctx, fam); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := s.Consume(ctx, h, now)
	assertCode(t, err, auth.CodeRefreshReused)
}

func TestRevokeSubject(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration")
	}
	ctx := context.Background()
	_, _ = testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens")
	s := New(testDB)
	now := time.Now().UTC()
	var h [32]byte
	h[0] = 1
	_ = s.Issue(ctx, auth.Record{TokenHash: h, Subject: "u-1",
		FamilyID: "55555555-5555-5555-5555-555555555555",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err := s.RevokeSubject(ctx, "u-1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := s.Consume(ctx, h, now)
	assertCode(t, err, auth.CodeRefreshReused)
}

func TestGarbageCollect(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration")
	}
	ctx := context.Background()
	_, _ = testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens")
	s := New(testDB)
	now := time.Now().UTC()
	var live, dead [32]byte
	live[0] = 1
	dead[0] = 2
	_ = s.Issue(ctx, auth.Record{TokenHash: live, FamilyID: "66666666-6666-6666-6666-666666666666",
		Subject: "u", IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	_ = s.Issue(ctx, auth.Record{TokenHash: dead, FamilyID: "77777777-7777-7777-7777-777777777777",
		Subject: "u", IssuedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)})
	n, err := s.GarbageCollect(ctx, now)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if n != 1 {
		t.Fatalf("gc removed %d, want 1", n)
	}
}

func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected code %q, got nil", want)
	}
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("err not *errs.Error: %v", err)
	}
	if e.Code != want {
		t.Fatalf("code = %q, want %q", e.Code, want)
	}
}
