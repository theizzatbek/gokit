package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed all:templates/init
var initFS embed.FS

const usageInit = `kit init — scaffold a new kit-based service

Usage:
  kit init <name> --module github.com/foo/bar [--dir .]

Examples:
  kit init tasks --module github.com/acme/tasks
  kit init tasks-api --module github.com/acme/tasks-api --dir services/tasks

Generates:
  go.mod, main.go, configs/routes.yaml, configs/clients.yaml,
  internal/handlers/handlers.go, Makefile, .gitignore, README.md

After running, cd into the new directory and run "go mod tidy" then
"make run". Service listens on :8080 with a /ping example route.
`

// initData feeds every template in templates/init/.
type initData struct {
	Name       string
	Module     string
	GoVersion  string
	KitVersion string
}

func runInit(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	module := fs.String("module", "", "go module path (required, e.g. github.com/acme/tasks)")
	dir := fs.String("dir", "", "destination directory (defaults to <name>)")
	goVer := fs.String("go-version", "1.23", "go directive in go.mod")
	kitVer := fs.String("kit-version", "v0.0.0", "version of github.com/theizzatbek/gokit to require")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageInit) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return errors.New("service name is required")
	}
	name := fs.Arg(0)
	if name == "" {
		return errors.New("service name is required")
	}
	if *module == "" {
		fs.Usage()
		return errors.New("--module is required")
	}
	target := *dir
	if target == "" {
		target = name
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", target, err)
	}
	if entries, _ := os.ReadDir(target); len(entries) > 0 {
		return fmt.Errorf("destination %s is non-empty; refusing to clobber existing files", target)
	}
	data := initData{
		Name:       name,
		Module:     *module,
		GoVersion:  *goVer,
		KitVersion: *kitVer,
	}
	if err := renderInitTemplates(target, data); err != nil {
		return err
	}
	fmt.Printf("kit init: created %s in %s\n", name, target)
	fmt.Printf("  cd %s\n", target)
	fmt.Println("  go mod tidy")
	fmt.Println("  make run")
	return nil
}

// renderInitTemplates walks templates/init/ and writes every file
// to target with the .tmpl suffix stripped.
func renderInitTemplates(target string, data initData) error {
	return fs.WalkDir(initFS, "templates/init", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := initFS.ReadFile(path)
		if err != nil {
			return err
		}
		tpl, err := template.New(path).Parse(string(raw))
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		rel := strings.TrimPrefix(path, "templates/init/")
		rel = strings.TrimSuffix(rel, ".tmpl")
		dest := filepath.Join(target, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		f, err := os.Create(dest)
		if err != nil {
			return err
		}
		if err := tpl.Execute(f, data); err != nil {
			f.Close()
			return fmt.Errorf("render %s: %w", path, err)
		}
		return f.Close()
	})
}
