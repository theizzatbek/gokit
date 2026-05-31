package migrate_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/migrate"
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

// freshDB returns a *db.DB against the test container with a unique
// schema set as search_path so each test sees an empty namespace.
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
	schema := fmt.Sprintf("mig_%d", time.Now().UnixNano())
	if _, err := d.Pool().Exec(context.Background(),
		fmt.Sprintf("CREATE SCHEMA %s; SET search_path TO %s", schema, schema)); err != nil {
		t.Fatal(err)
	}
	return d
}

// memFS builds a virtual file system from the supplied name → contents
// map. Used to keep tests self-contained instead of shipping fixture
// files alongside them.
func memFS(files map[string]string) fstest.MapFS {
	fs := fstest.MapFS{}
	for name, body := range files {
		fs[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fs
}

func TestUp_AppliesPendingThenSkipsOnRerun(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	fs := memFS(map[string]string{
		"0001_init.sql":   `CREATE TABLE widgets (id int PRIMARY KEY)`,
		"0002_extend.sql": `ALTER TABLE widgets ADD COLUMN name text`,
	})

	if err := migrate.Up(ctx, d, fs); err != nil {
		t.Fatalf("Up: %v", err)
	}
	v, err := migrate.Version(ctx, d)
	if err != nil || v != "0002" {
		t.Fatalf("Version = (%q, %v), want (\"0002\", nil)", v, err)
	}

	// Rerun must be a no-op.
	if err := migrate.Up(ctx, d, fs); err != nil {
		t.Fatalf("Up (rerun): %v", err)
	}
	v2, _ := migrate.Version(ctx, d)
	if v2 != "0002" {
		t.Errorf("Version after rerun = %q, want 0002", v2)
	}
}

func TestUp_PartialAppliesThenAppendsNewMigration(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	fsBefore := memFS(map[string]string{
		"0001_init.sql": `CREATE TABLE widgets (id int PRIMARY KEY)`,
	})
	if err := migrate.Up(ctx, d, fsBefore); err != nil {
		t.Fatalf("Up #1: %v", err)
	}

	fsAfter := memFS(map[string]string{
		"0001_init.sql":   `CREATE TABLE widgets (id int PRIMARY KEY)`,
		"0002_extend.sql": `ALTER TABLE widgets ADD COLUMN name text`,
	})
	if err := migrate.Up(ctx, d, fsAfter); err != nil {
		t.Fatalf("Up #2: %v", err)
	}
	v, _ := migrate.Version(ctx, d)
	if v != "0002" {
		t.Errorf("Version = %q, want 0002", v)
	}
}

func TestDown_RollsBackNMigrations(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	fs := memFS(map[string]string{
		"0001_init.sql":        `CREATE TABLE widgets (id int PRIMARY KEY)`,
		"0001_init.down.sql":   `DROP TABLE widgets`,
		"0002_extend.sql":      `ALTER TABLE widgets ADD COLUMN name text`,
		"0002_extend.down.sql": `ALTER TABLE widgets DROP COLUMN name`,
	})
	if err := migrate.Up(ctx, d, fs); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := migrate.Down(ctx, d, fs, 1); err != nil {
		t.Fatalf("Down 1: %v", err)
	}
	v, _ := migrate.Version(ctx, d)
	if v != "0001" {
		t.Errorf("Version after Down 1 = %q, want 0001", v)
	}

	if err := migrate.Down(ctx, d, fs, 1); err != nil {
		t.Fatalf("Down 1 again: %v", err)
	}
	v2, _ := migrate.Version(ctx, d)
	if v2 != "" {
		t.Errorf("Version after Down all = %q, want empty", v2)
	}
}

func TestDown_ZeroIsNoOp(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	if err := migrate.Down(ctx, d, memFS(nil), 0); err != nil {
		t.Errorf("Down 0: %v", err)
	}
}

func TestDown_MissingDownFile_Errors(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	fs := memFS(map[string]string{
		"0001_init.sql": `CREATE TABLE widgets (id int PRIMARY KEY)`,
		// no .down.sql for 0001
	})
	if err := migrate.Up(ctx, d, fs); err != nil {
		t.Fatalf("Up: %v", err)
	}
	err := migrate.Down(ctx, d, fs, 1)
	if err == nil {
		t.Fatal("expected error")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != migrate.CodeUnknownDown {
		t.Errorf("err = %v, want CodeUnknownDown", err)
	}
}

func TestParse_MalformedSQLFileErrors(t *testing.T) {
	fs := memFS(map[string]string{
		"not_a_version.sql": `SELECT 1`,
	})
	_, _, err := migrate.Parse(fs)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != migrate.CodeInvalidFilename {
		t.Errorf("err = %v, want CodeInvalidFilename", err)
	}
}

func TestParse_DuplicateVersionErrors(t *testing.T) {
	fs := memFS(map[string]string{
		"0001_init.sql":  `SELECT 1`,
		"0001_again.sql": `SELECT 2`,
	})
	_, _, err := migrate.Parse(fs)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != migrate.CodeDuplicateVersion {
		t.Errorf("err = %v, want CodeDuplicateVersion", err)
	}
}

func TestParse_OrphanDownErrors(t *testing.T) {
	fs := memFS(map[string]string{
		"0001_init.down.sql": `DROP TABLE x`,
	})
	_, _, err := migrate.Parse(fs)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != migrate.CodeOrphanDown {
		t.Errorf("err = %v, want CodeOrphanDown", err)
	}
}

func TestParse_NoTransactionDirectiveDetected(t *testing.T) {
	fs := memFS(map[string]string{
		"0001_concurrent.sql": "-- @migrate:no-transaction\nCREATE INDEX CONCURRENTLY foo_idx ON widgets(id)",
	})
	ups, _, err := migrate.Parse(fs)
	if err != nil {
		t.Fatal(err)
	}
	if !ups[0].NoTransaction {
		t.Error("directive should be detected on the first comment line")
	}
}

func TestParse_NonSQLFilesIgnored(t *testing.T) {
	fs := memFS(map[string]string{
		"0001_init.sql": `SELECT 1`,
		"README.md":     `# docs`,
	})
	ups, _, err := migrate.Parse(fs)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 {
		t.Errorf("len(ups) = %d, want 1 (README.md should be ignored)", len(ups))
	}
}

func TestList_ReportsAppliedFlag(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	fs := memFS(map[string]string{
		"0001_init.sql":   `CREATE TABLE widgets (id int PRIMARY KEY)`,
		"0002_extend.sql": `ALTER TABLE widgets ADD COLUMN name text`,
	})
	if err := migrate.Up(ctx, d, memFS(map[string]string{
		"0001_init.sql": `CREATE TABLE widgets (id int PRIMARY KEY)`,
	})); err != nil {
		t.Fatal(err)
	}
	st, err := migrate.List(ctx, d, fs)
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 {
		t.Fatalf("len = %d, want 2", len(st))
	}
	if !st[0].Applied || st[1].Applied {
		t.Errorf("applied flags = %+v, want (true, false)", st)
	}
}

func TestVersion_EmptyBeforeAnyUp(t *testing.T) {
	d := freshDB(t)
	v, err := migrate.Version(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Errorf("Version = %q, want empty", v)
	}
}
