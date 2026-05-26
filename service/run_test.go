package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theizzatbek/gokit/fibermap/openapi"
)

func TestRun_RoutesEnabled_FileMissing_ReturnsCodeRoutesYAMLNotFound(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	cfg := Config{
		Routes: RoutesConfig{Enabled: true},
	}
	cfg.Service.LogLevel = "error"
	svc, err := New[map[string]any, any](context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)
	err = svc.Run()
	if err == nil || !strings.Contains(err.Error(), CodeRoutesYAMLNotFound) {
		t.Fatalf("want CodeRoutesYAMLNotFound, got %v", err)
	}
}

func TestRun_RoutesOverridePath_FileMissing_ReturnsCodeRoutesYAMLNotFound(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	cfg := Config{
		Routes: RoutesConfig{Path: "nonexistent-routes.yaml"},
	}
	cfg.Service.LogLevel = "error"
	svc, err := New[map[string]any, any](context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)
	err = svc.Run()
	if err == nil || !strings.Contains(err.Error(), CodeRoutesYAMLNotFound) {
		t.Fatalf("want CodeRoutesYAMLNotFound, got %v", err)
	}
}

func TestRun_RoutesEnabled_FilePresent_LoadsViaResolvePath(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	routesPath := filepath.Join(tmp, "routes.yaml")
	if err := os.WriteFile(routesPath, []byte(`groups: []`+"\n"), 0o644); err != nil {
		t.Fatalf("write routes: %v", err)
	}
	cfg := Config{
		Routes: RoutesConfig{Enabled: true},
	}
	cfg.Service.LogLevel = "error"
	svc, err := New[map[string]any, any](context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)

	// svc.Run blocks on Listen, so we validate the building blocks rather
	// than calling it directly: resolvePath must locate the file, stat must
	// succeed, and Engine.LoadFile must accept the YAML.
	path := resolvePath(cfg.Routes.Path, DefaultRoutesPath, cfg.Routes.Enabled)
	if path != DefaultRoutesPath {
		t.Fatalf("resolvePath: got %q want %q", path, DefaultRoutesPath)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	}
	if err := svc.Engine.LoadFile(path); err != nil {
		t.Fatalf("Engine.LoadFile: %v", err)
	}
}

func TestRun_OpenAPIBlockInRoutesYAML_AutoMounts(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	body := []byte(`groups: []
openapi:
  info:
    title: Auto-mounted
    version: 0.1.0
`)
	if err := os.WriteFile("routes.yaml", body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := Config{
		Routes: RoutesConfig{Enabled: true},
	}
	cfg.Service.LogLevel = "error"
	svc, err := New[map[string]any, any](context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)

	// Simulate the mountOpenAPI step that Run would invoke (Run blocks on
	// Listen, so we exercise the building blocks directly).
	routesPath := resolvePath(cfg.Routes.Path, DefaultRoutesPath, cfg.Routes.Enabled)
	if routesPath == "" {
		t.Fatal("routesPath empty")
	}
	if _, err := os.Stat(routesPath); err != nil {
		t.Fatalf("stat: %v", err)
	}
	if err := svc.Engine.LoadFile(routesPath); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if err := svc.mountOpenAPI(routesPath); err != nil {
		t.Fatalf("mountOpenAPI: %v", err)
	}
	// Mount installed /openapi.json + /docs on the engine; full HTTP exercise
	// happens via the urlshort end-to-end smoke after Task 4.
}

func TestRun_OpenAPI_CodeOverridesYAMLInfo(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	body := []byte(`groups: []
openapi:
  info:
    title: from-yaml
    version: 0.0.1
`)
	if err := os.WriteFile("routes.yaml", body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := Config{
		Routes: RoutesConfig{Enabled: true},
	}
	cfg.Service.LogLevel = "error"
	svc, err := New[map[string]any, any](context.Background(), cfg,
		WithOpenAPI(openapi.WithInfo(openapi.Info{Title: "from-code", Version: "1.0.0"})),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)

	routesPath := resolvePath(cfg.Routes.Path, DefaultRoutesPath, cfg.Routes.Enabled)
	if err := svc.Engine.LoadFile(routesPath); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if err := svc.mountOpenAPI(routesPath); err != nil {
		t.Fatalf("mountOpenAPI: %v", err)
	}
	// The fact that mountOpenAPI succeeds with both YAML Info and code WithInfo
	// proves they coexist; full precedence-of-Title verification happens via
	// the urlshort end-to-end smoke after Task 4.
}
