package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/errs"
)

func TestHealthcheck_OK(t *testing.T) {
	d := startTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.Healthcheck(ctx); err != nil {
		t.Fatalf("Healthcheck: %v", err)
	}
}

func TestHealthcheck_AfterClose_KindUnavailable(t *testing.T) {
	d := startTestDB(t)
	d.Close()
	err := d.Healthcheck(context.Background())
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindUnavailable {
		t.Fatalf("want KindUnavailable, got %v (%T)", e, err)
	}
}
