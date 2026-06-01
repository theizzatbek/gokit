package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/theizzatbek/gokit/db"
)

// loadDB opens a *db.DB from --dsn flag OR DATABASE_URL env. Returns
// a helpful error when neither is supplied so the caller can print
// a usage hint.
func loadDB(ctx context.Context, dsn string) (*db.DB, error) {
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		return nil, errors.New("missing --dsn flag or DATABASE_URL env")
	}
	cfg, err := parseDSN(dsn)
	if err != nil {
		return nil, err
	}
	return db.Connect(ctx, cfg)
}

// parseDSN converts a postgres URL to the kit's db.Config.
//
// pgx accepts the URL as-is, but the kit's Connect constructs a URL
// from individual fields. We split the URL once so the same DSN
// reaches Connect's pool config + any kit-specific knobs the URL
// doesn't carry (pool sizing, retries).
func parseDSN(dsn string) (db.Config, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return db.Config{}, err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return db.Config{}, errors.New("dsn scheme must be postgres:// or postgresql://")
	}
	cfg := db.Config{
		Host:           u.Hostname(),
		User:           u.User.Username(),
		Database:       u.Path,
		SSLMode:        "disable",
		ConnectTimeout: 5 * time.Second,
		MaxConns:       2,
		MinConns:       1,
	}
	if cfg.Database != "" && cfg.Database[0] == '/' {
		cfg.Database = cfg.Database[1:]
	}
	if pw, ok := u.User.Password(); ok {
		cfg.Password = pw
	}
	if port := u.Port(); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.Port = p
		}
	}
	if cfg.Port == 0 {
		cfg.Port = 5432
	}
	q := u.Query()
	if mode := q.Get("sslmode"); mode != "" {
		cfg.SSLMode = mode
	}
	if app := q.Get("application_name"); app != "" {
		cfg.AppName = app
	}
	return cfg, nil
}
