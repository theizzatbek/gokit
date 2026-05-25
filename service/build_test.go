package service

import (
	"context"
	"errors"
	"testing"

	xerrs "github.com/theizzatbek/gokit/errs"
)

type testCtx struct{}
type testClaims struct{}

func TestNew_AllSubsystemsOff_OnlyEngineAndHTTPC(t *testing.T) {
	svc, err := New[testCtx, testClaims](context.Background(), Config{})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	t.Cleanup(svc.Close)

	if svc.Engine == nil {
		t.Error("Engine is nil — should always be built")
	}
	if svc.HTTPC == nil {
		t.Error("HTTPC is nil — should always be built")
	}
	if svc.DB != nil {
		t.Error("DB should be nil with empty config")
	}
	if svc.Auth != nil {
		t.Error("Auth should be nil with empty config")
	}
	if svc.NATS != nil {
		t.Error("NATS should be nil with empty config")
	}
	if svc.APIMap != nil {
		t.Error("APIMap should be nil with empty config")
	}
	if svc.Hasher != nil {
		t.Error("Hasher should be nil without Auth")
	}
}

func TestNew_AuthWithoutDB_ReturnsCodeAuthNeedsDB(t *testing.T) {
	cfg := Config{Auth: AuthConfig{PrivateKeyPEM: "x"}}
	_, err := New[testCtx, testClaims](context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeAuthNeedsDB {
		t.Errorf("err = %v, want code %q", err, CodeAuthNeedsDB)
	}
}

func TestNew_APIMapLoadFailure_PropagatesAsCodeAPIMapLoadFailed(t *testing.T) {
	cfg := Config{APIMap: APIMapConfig{Path: "nonexistent-file.yaml"}}
	_, err := New[testCtx, testClaims](context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeAPIMapLoadFailed {
		t.Errorf("err = %v, want code %q", err, CodeAPIMapLoadFailed)
	}
}
