package service

import (
	"context"
	"errors"
	"os"
	"strings"
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
	if !errors.As(err, &e) || e.Code != CodeAPIMapYAMLNotFound {
		t.Errorf("err = %v, want code %q", err, CodeAPIMapYAMLNotFound)
	}
}

func TestNew_APIMapEnabled_FileMissing_ReturnsCodeAPIMapYAMLNotFound(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	cfg := Config{}
	cfg.APIMap.Enabled = true
	_, err := New[map[string]any, any](context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), CodeAPIMapYAMLNotFound) {
		t.Fatalf("want CodeAPIMapYAMLNotFound, got %v", err)
	}
}

func TestNew_NATSMapEnabled_BothFilesMissing_ReturnsCodeNATSMapYAMLNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — Docker required")
	}
	tmp := t.TempDir()
	t.Chdir(tmp)
	natsURL := startSmokeNATS(t, context.Background())
	cfg := Config{
		NATS:    NATSConfig{URL: natsURL},
		NATSMap: NATSMapConfig{Enabled: true},
	}
	cfg.Service.LogLevel = "error"
	_, err := New[map[string]any, any](context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), CodeNATSMapYAMLNotFound) {
		t.Fatalf("want CodeNATSMapYAMLNotFound, got %v", err)
	}
}

func TestNew_NATSMapOverridePath_FileMissing_ReturnsCodeNATSMapYAMLNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — Docker required")
	}
	tmp := t.TempDir()
	t.Chdir(tmp)
	natsURL := startSmokeNATS(t, context.Background())
	cfg := Config{
		NATS:    NATSConfig{URL: natsURL},
		NATSMap: NATSMapConfig{SubscribersPath: "nonexistent.yaml"},
	}
	cfg.Service.LogLevel = "error"
	_, err := New[map[string]any, any](context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), CodeNATSMapYAMLNotFound) {
		t.Fatalf("want CodeNATSMapYAMLNotFound, got %v", err)
	}
}

func TestNew_WithAPIMap_DefaultFilePresent_Builds(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	if err := os.WriteFile("clients.yaml", []byte(`clients:
  - name: x
    base_url: https://example.com
    endpoints:
      - {name: get, method: GET, path: /, decode: json}
`), 0o644); err != nil {
		t.Fatalf("write clients.yaml: %v", err)
	}
	svc, err := New[map[string]any, any](context.Background(), Config{}, WithAPIMap())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)
	if svc.APIMap == nil {
		t.Fatal("svc.APIMap == nil; WithAPIMap() did not trigger build")
	}
}

func TestNew_WithAPIMap_FileMissing_ReturnsCodeAPIMapYAMLNotFound(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	_, err := New[map[string]any, any](context.Background(), Config{}, WithAPIMap())
	if err == nil || !strings.Contains(err.Error(), CodeAPIMapYAMLNotFound) {
		t.Fatalf("want CodeAPIMapYAMLNotFound, got %v", err)
	}
}

func TestNew_WithNATSMap_WithoutNATS_ReturnsCodeNATSMapNeedsNATS(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	if err := os.WriteFile("publishers.yaml", []byte(`publishers:
  - {name: p, subject: test.x}
`), 0o644); err != nil {
		t.Fatalf("write publishers.yaml: %v", err)
	}
	_, err := New[map[string]any, any](context.Background(), Config{}, WithNATSMap())
	if err == nil || !strings.Contains(err.Error(), CodeNATSMapNeedsNATS) {
		t.Fatalf("want CodeNATSMapNeedsNATS, got %v", err)
	}
}

func TestNew_NodeName_DefaultsToHostname(t *testing.T) {
	cfg := Config{}
	cfg.Service.LogLevel = "error"
	svc, err := New[map[string]any, any](context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)
	want, _ := os.Hostname()
	if svc.cfg.Service.NodeName != want {
		t.Fatalf("NodeName: got %q want hostname %q", svc.cfg.Service.NodeName, want)
	}
}

func TestNew_NodeName_ExplicitPreserved(t *testing.T) {
	cfg := Config{}
	cfg.Service.LogLevel = "error"
	cfg.Service.NodeName = "explicit-node"
	svc, err := New[map[string]any, any](context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)
	if svc.cfg.Service.NodeName != "explicit-node" {
		t.Fatalf("NodeName: got %q want %q", svc.cfg.Service.NodeName, "explicit-node")
	}
}
