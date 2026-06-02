package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeYAML(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWithCronMap_DefaultPathMissing_NoOp(t *testing.T) {
	t.Parallel()
	// No crons.yaml exists at default path → svc.CronMap stays nil
	// without erroring (allows a service to add jobs later).
	tmp := t.TempDir()
	cfg := Config{}
	cfg.Service.ConfigsDir = tmp

	svc, err := New[map[string]any, any](context.Background(), cfg, WithCronMap())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)
	if svc.CronMap != nil {
		t.Errorf("CronMap should be nil when default-path file is missing")
	}
}

func TestWithCronMap_ExplicitPathMissing_Errors(t *testing.T) {
	t.Parallel()
	cfg := Config{}
	cfg.CronMap.Path = "/nonexistent/path/crons.yaml"

	_, err := New[map[string]any, any](context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for missing explicit Path")
	}
	if !strings.Contains(err.Error(), CodeCronMapYAMLNotFound) {
		t.Errorf("err = %v, want %q", err, CodeCronMapYAMLNotFound)
	}
}

func TestWithCronMap_RegisterCronHandler_RoutesToEngine(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	writeYAML(t, tmp, "crons.yaml", `
jobs:
  - name: tick
    handler: tick
    schedule: "0 3 * * *"
`)
	cfg := Config{}
	cfg.Service.ConfigsDir = tmp

	var hits atomic.Int64
	svc, err := New[map[string]any, any](context.Background(), cfg,
		WithCronMap(),
		RegisterCronHandler("tick", func(context.Context) error {
			hits.Add(1)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)

	if svc.CronMap == nil {
		t.Fatal("CronMap should be non-nil after WithCronMap + present YAML")
	}
	if got := svc.CronMap.JobNames(); len(got) != 1 || got[0] != "tick" {
		t.Errorf("JobNames = %v, want [tick]", got)
	}
	// Schedule is "0 3 * * *" — fires at 03:00; the test doesn't
	// wait for a tick. We just assert the registration plumbing
	// succeeded by checking JobNames above. hits is read so the
	// counter doesn't get optimised away (vet flags pointer-copies
	// of atomic.Int64).
	_ = hits.Load()
}

func TestWithCronMap_SingletonNeedsDB(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	writeYAML(t, tmp, "crons.yaml", `
jobs:
  - name: leader
    handler: leader
    schedule: "0 3 * * *"
    singleton: true
`)
	cfg := Config{}
	cfg.Service.ConfigsDir = tmp

	_, err := New[map[string]any, any](context.Background(), cfg,
		WithCronMap(),
		RegisterCronHandler("leader", func(context.Context) error { return nil }),
	)
	if err == nil {
		t.Fatal("expected error: singleton job without DB")
	}
	if !strings.Contains(err.Error(), CodeCronMapNeedsDB) {
		t.Errorf("err = %v, want %q", err, CodeCronMapNeedsDB)
	}
}

func TestWithCronMap_EnabledViaConfigOnly(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	writeYAML(t, tmp, "crons.yaml", `jobs: []`)
	cfg := Config{}
	cfg.CronMap.Enabled = true
	cfg.Service.ConfigsDir = tmp

	svc, err := New[map[string]any, any](context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)
	if svc.CronMap == nil {
		t.Fatal("CronMap should build from Config.CronMap.Enabled=true alone")
	}
}

func TestWithCronMap_EnvSubstitution(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	writeYAML(t, tmp, "crons.yaml", `
jobs:
  - name: nightly
    handler: nightly
    schedule: "0 ${NIGHTLY_HOUR} * * *"
`)
	cfg := Config{}
	cfg.Service.ConfigsDir = tmp

	svc, err := New[map[string]any, any](context.Background(), cfg,
		WithCronMap(),
		WithCronMapEnv(map[string]string{"NIGHTLY_HOUR": "2"}),
		RegisterCronHandler("nightly", func(context.Context) error { return nil }),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)
}

func TestStatus_CronMapField(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	writeYAML(t, tmp, "crons.yaml", `
jobs:
  - name: a
    handler: a
    schedule: "0 3 * * *"
  - name: b
    handler: b
    schedule: "0 4 * * *"
`)
	cfg := Config{}
	cfg.Service.ConfigsDir = tmp

	svc, err := New[map[string]any, any](context.Background(), cfg,
		WithCronMap(),
		RegisterCronHandler("a", func(context.Context) error { return nil }),
		RegisterCronHandler("b", func(context.Context) error { return nil }),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)
	if got := svc.Status().CronMap; got != 2 {
		t.Errorf("Status().CronMap = %d, want 2", got)
	}
}

func TestWithCronMap_CloseDrainsRuntime(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	writeYAML(t, tmp, "crons.yaml", `jobs: []`)
	cfg := Config{}
	cfg.CronMap.Enabled = true
	cfg.Service.ConfigsDir = tmp

	svc, err := New[map[string]any, any](context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Close must return promptly even with an empty runtime; the
	// 5s default Stop deadline should never trigger.
	done := make(chan struct{})
	go func() {
		svc.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return within 3s; CronMap Stop wired wrong?")
	}
}
