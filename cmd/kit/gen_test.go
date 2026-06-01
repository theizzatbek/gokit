package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenMigration_CreatesPair(t *testing.T) {
	tmp := t.TempDir()
	err := runGenMigration(context.Background(), []string{
		"--dir", tmp, "add_user_index",
	})
	if err != nil {
		t.Fatalf("runGenMigration: %v", err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 files, got %d", len(entries))
	}
	var foundUp, foundDown bool
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			foundUp = true
		}
		if strings.HasSuffix(e.Name(), ".down.sql") {
			foundDown = true
		}
		if !strings.Contains(e.Name(), "add_user_index") {
			t.Errorf("filename missing name: %q", e.Name())
		}
	}
	if !foundUp || !foundDown {
		t.Errorf("missing up/down: %v", entries)
	}
}

func TestGenMigration_RejectsBadName(t *testing.T) {
	tmp := t.TempDir()
	err := runGenMigration(context.Background(), []string{
		"--dir", tmp, "Add-Index!",
	})
	if err == nil {
		t.Error("expected error for bad name")
	}
}

func TestGenMigration_RefusesClobber(t *testing.T) {
	tmp := t.TempDir()
	err := runGenMigration(context.Background(), []string{
		"--dir", tmp, "add_index",
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	// Force a clobber: write a file with the same timestamp+name.
	entries, _ := os.ReadDir(tmp)
	if len(entries) == 0 {
		t.Fatal("no files written")
	}
	// Rerun immediately — same timestamp → refuses.
	err = runGenMigration(context.Background(), []string{
		"--dir", tmp, "add_index",
	})
	if err == nil {
		t.Error("expected clobber refusal on same-second rerun")
	}
}

func TestGenK8s_EmitsExpectedSections(t *testing.T) {
	out := filepath.Join(t.TempDir(), "manifests.yaml")
	err := runGenK8s(context.Background(), []string{
		"--name", "tasks",
		"--image", "ghcr.io/acme/tasks:1.0",
		"--namespace", "prod",
		"--port", "3000",
		"--host", "tasks.example.com",
		"--out", out,
	})
	if err != nil {
		t.Fatalf("runGenK8s: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(body)
	for _, want := range []string{
		"kind: Deployment",
		"kind: Service",
		"kind: ConfigMap",
		"kind: Ingress",
		"namespace: prod",
		"ghcr.io/acme/tasks:1.0",
		"path: /readyz",
		"path: /healthz",
		"path: /preflight",
		"tasks.example.com",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q", want)
		}
	}
}

func TestGenK8s_NoIngressWithoutHost(t *testing.T) {
	var buf bytes.Buffer
	if err := emitK8sManifests(&buf, k8sFlags{
		name:     "tasks",
		image:    "img:latest",
		replicas: 1,
		port:     3000,
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if strings.Contains(buf.String(), "kind: Ingress") {
		t.Errorf("Ingress emitted without --host: %s", buf.String())
	}
}

func TestGenK8s_RequiresNameAndImage(t *testing.T) {
	err := runGenK8s(context.Background(), []string{"--image", "img"})
	if err == nil {
		t.Error("expected error missing --name")
	}
	err = runGenK8s(context.Background(), []string{"--name", "svc"})
	if err == nil {
		t.Error("expected error missing --image")
	}
}

func TestGenK8s_ConfigMapHasKitEnvVars(t *testing.T) {
	var buf bytes.Buffer
	if err := emitK8sManifests(&buf, k8sFlags{
		name: "tasks", image: "img", replicas: 1, port: 3000,
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	s := buf.String()
	// Reflection over service.Config should produce some known env vars.
	// At minimum check ADDR exists (from ServiceConfig.Addr).
	if !strings.Contains(s, "ADDR:") {
		t.Errorf("ConfigMap missing ADDR (kit-known env): %s", s)
	}
}
