package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
