package s3mod_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/theizzatbek/gokit/svckit"
	"github.com/theizzatbek/gokit/svckit/mods/s3mod"
)

func TestMod_DisabledWhenBucketEmpty(t *testing.T) {
	m := s3mod.New(s3mod.Config{}) // Bucket empty -> operator did not enable S3

	svc, err := svckit.New[struct{}, struct{}](context.Background(), svckit.Config{}, m.Option())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	if m.Enabled() {
		t.Error("Enabled(): want false when Bucket is empty")
	}
	if _, ok := m.Optional(); ok {
		t.Error("Optional(): want ok=false")
	}
}

func TestMod_ClientPanicsNamingTheEnv(t *testing.T) {
	m := s3mod.New(s3mod.Config{})
	svc, err := svckit.New[struct{}, struct{}](context.Background(), svckit.Config{}, m.Option())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Client() must panic when the mod is disabled")
		}
		if !strings.Contains(fmt.Sprint(r), "S3_BUCKET") {
			t.Errorf("panic must name the env var: got %v", r)
		}
	}()
	_ = m.Client()
}

func TestMod_ClientPanicsBeforeNew(t *testing.T) {
	m := s3mod.New(s3mod.Config{Bucket: "b"}) // not passed to svckit.New

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Client() before build must panic")
		}
		if !strings.Contains(fmt.Sprint(r), "svckit.New") {
			t.Errorf("panic must explain the mod wasn't passed to New: got %v", r)
		}
	}()
	_ = m.Client()
}

func TestMod_NameIsOverridable(t *testing.T) {
	if got := s3mod.New(s3mod.Config{}).Name(); got != "s3" {
		t.Errorf("Name(): want s3, got %q", got)
	}
	if got := s3mod.New(s3mod.Config{}, s3mod.WithName("backup")).Name(); got != "backup" {
		t.Errorf("Name() with WithName: want backup, got %q", got)
	}
}

func TestMod_TwoInstancesCoexist(t *testing.T) {
	primary := s3mod.New(s3mod.Config{})
	backup := s3mod.New(s3mod.Config{}, s3mod.WithName("s3-backup"))

	svc, err := svckit.New[struct{}, struct{}](context.Background(), svckit.Config{},
		primary.Option(), backup.Option())
	if err != nil {
		t.Fatalf("two mods with different names must coexist: %v", err)
	}
	defer svc.Close()

	st := svc.Status()
	if len(st.Mods) != 2 {
		t.Fatalf("Status.Mods: want 2, got %d", len(st.Mods))
	}
}
