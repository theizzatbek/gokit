package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/theizzatbek/fibermap/db"
	"github.com/theizzatbek/fibermap/errs"
)

func TestConnect_BadCredentials_KindUnavailable(t *testing.T) {
	startTestDB(t) // ensure container is up; we then point at it with bad creds
	cfg := pgCfg
	cfg.Password = "wrong-password"
	_, err := db.Connect(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindUnavailable {
		t.Fatalf("expected KindUnavailable, got %v / %T", e, err)
	}
}

func TestConnect_Success_PoolReturnsHandle(t *testing.T) {
	d := startTestDB(t)
	if d.Pool() == nil {
		t.Fatal("Pool() returned nil")
	}
}

func TestClose_Idempotent(t *testing.T) {
	d := startTestDB(t)
	d.Close()
	d.Close() // must not panic
}
