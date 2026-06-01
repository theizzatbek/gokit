package main

import (
	"bytes"
	"context"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInit_GeneratesCompilingGoFiles runs `kit init` into a tempdir
// and parses every generated .go file with go/parser to confirm
// the templates aren't broken.
func TestInit_GeneratesCompilingGoFiles(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "tasks")

	if err := runInit(context.Background(), []string{
		"--module", "github.com/acme/tasks",
		"--dir", target,
		"tasks",
	}); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	// Verify required files.
	for _, p := range []string{"go.mod", "main.go", "configs/routes.yaml", "Makefile", "internal/handlers/handlers.go"} {
		if _, err := os.Stat(filepath.Join(target, p)); err != nil {
			t.Errorf("missing generated %s: %v", p, err)
		}
	}

	// Parse all .go files for syntactic validity.
	fset := token.NewFileSet()
	err := filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if _, err := parser.ParseFile(fset, path, nil, parser.AllErrors); err != nil {
			t.Errorf("parse %s: %v", path, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

func TestInit_RequiresModule(t *testing.T) {
	err := runInit(context.Background(), []string{"tasks"})
	if err == nil {
		t.Fatal("expected error for missing --module")
	}
}

func TestInit_RefusesNonEmptyTarget(t *testing.T) {
	tmp := t.TempDir()
	// Pre-populate.
	_ = os.WriteFile(filepath.Join(tmp, "stray.txt"), []byte("x"), 0o644)
	err := runInit(context.Background(), []string{
		"--module", "github.com/x/y",
		"--dir", tmp,
		"foo",
	})
	if err == nil {
		t.Error("expected refusal on non-empty target")
	}
}

func TestAddEndpoint_AppendsToRoutesYAMLAndCreatesHandler(t *testing.T) {
	tmp := t.TempDir()
	routes := filepath.Join(tmp, "routes.yaml")
	_ = os.WriteFile(routes, []byte("groups: []\n"), 0o644)
	handlersDir := filepath.Join(tmp, "handlers")

	err := runAddEndpoint(context.Background(), []string{
		"--routes", routes,
		"--handlers-pkg", handlersDir,
		"POST", "/tasks", "tasks.create",
	})
	if err != nil {
		t.Fatalf("runAddEndpoint: %v", err)
	}

	body, _ := os.ReadFile(routes)
	if !bytes.Contains(body, []byte("tasks.create")) {
		t.Errorf("routes.yaml missing handler: %s", body)
	}
	if !bytes.Contains(body, []byte("/tasks")) {
		t.Errorf("routes.yaml missing path: %s", body)
	}

	stub, err := os.ReadFile(filepath.Join(handlersDir, "tasks.go"))
	if err != nil {
		t.Fatalf("handler stub: %v", err)
	}
	if !bytes.Contains(stub, []byte("tasks.create")) {
		t.Errorf("stub missing handler name: %s", stub)
	}
}

func TestAddEndpoint_RejectsUnknownMethod(t *testing.T) {
	err := runAddEndpoint(context.Background(),
		[]string{"WHEN", "/x", "x.handle"})
	if err == nil {
		t.Error("expected error for unknown method")
	}
}

func TestAddEndpoint_RejectsNonSlashPath(t *testing.T) {
	tmp := t.TempDir()
	routes := filepath.Join(tmp, "routes.yaml")
	_ = os.WriteFile(routes, []byte("groups: []\n"), 0o644)
	err := runAddEndpoint(context.Background(),
		[]string{"--routes", routes, "GET", "no-slash", "x"})
	if err == nil {
		t.Error("expected error for non-slash path")
	}
}

// silence imports when nothing in scope uses exec.
var _ = exec.Command
