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

	"github.com/theizzatbek/fibermap/auth"
	"github.com/theizzatbek/fibermap/db"
	"github.com/theizzatbek/fibermap/errs"
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
		return m.Run()
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
