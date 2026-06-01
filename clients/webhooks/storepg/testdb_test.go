package storepg

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/db"
)

var testDB *db.DB

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		return 0
	}
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("webhookstest"),
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
	if _, err := d.Exec(ctx, Schema()); err != nil {
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

func newTestKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}
