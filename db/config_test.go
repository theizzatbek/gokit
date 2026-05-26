package db

import (
	"strings"
	"testing"
	"time"
)

func TestBuildPgxURL_URLOverridesAssembledFields(t *testing.T) {
	got, err := buildPgxURL(Config{
		URL:      "postgres://app:pass@cluster:5432/appdb?sslmode=require",
		Host:     "ignored",
		Port:     9999,
		User:     "ignored",
		Password: "ignored",
		Database: "ignored",
		SSLMode:  "disable",
	})
	if err != nil {
		t.Fatalf("buildPgxURL: %v", err)
	}
	if !strings.Contains(got, "@cluster:5432/appdb") {
		t.Fatalf("URL not used: %q", got)
	}
	if strings.Contains(got, "ignored") {
		t.Fatalf("assembled fields leaked: %q", got)
	}
	if !strings.Contains(got, "sslmode=require") {
		t.Fatalf("user query params dropped: %q", got)
	}
}

func TestBuildPgxURL_AssembledFieldsWhenURLEmpty(t *testing.T) {
	got, err := buildPgxURL(Config{
		Host: "db.internal", Port: 5432,
		User: "alice", Password: "s3cret",
		Database: "app", SSLMode: "disable",
	})
	if err != nil {
		t.Fatalf("buildPgxURL: %v", err)
	}
	want := "postgres://alice:s3cret@db.internal:5432/app?sslmode=disable"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBuildPgxURL_MultiHostPreserved(t *testing.T) {
	got, err := buildPgxURL(Config{
		URL: "postgres://app:pass@h1,h2,h3:5432/appdb?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("buildPgxURL: %v", err)
	}
	if !strings.Contains(got, "h1,h2,h3:5432") {
		t.Fatalf("multi-host dropped: %q", got)
	}
}

func TestBuildPgxURL_MalformedURLReturnsError(t *testing.T) {
	_, err := buildPgxURL(Config{URL: "::::not-a-url"})
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestBuildPgxURL_MinimumRequiredFields(t *testing.T) {
	got, err := buildPgxURL(Config{
		Host: "db.internal", Port: 5432,
		User: "alice", Password: "s3cret",
		Database: "app", SSLMode: "disable",
	})
	if err != nil {
		t.Fatalf("buildPgxURL: %v", err)
	}
	want := "postgres://alice:s3cret@db.internal:5432/app?sslmode=disable"
	if got != want {
		t.Fatalf("buildPgxURL = %q, want %q", got, want)
	}
}

func TestBuildPgxURL_IncludesAppNameAndTimeoutWhenSet(t *testing.T) {
	got, err := buildPgxURL(Config{
		Host: "h", Port: 1, User: "u", Database: "d",
		SSLMode: "require", AppName: "checkout-api",
		ConnectTimeout: 7 * time.Second,
	})
	if err != nil {
		t.Fatalf("buildPgxURL: %v", err)
	}
	if !strings.Contains(got, "application_name=checkout-api") {
		t.Fatalf("DSN missing application_name: %q", got)
	}
	if !strings.Contains(got, "connect_timeout=7") {
		t.Fatalf("DSN missing connect_timeout: %q", got)
	}
	if !strings.Contains(got, "sslmode=require") {
		t.Fatalf("DSN missing sslmode=require: %q", got)
	}
}

func TestBuildPgxURL_EscapesPasswordSpecialChars(t *testing.T) {
	got, err := buildPgxURL(Config{
		Host: "h", Port: 1, User: "u", Password: "p@ss/word",
		Database: "d", SSLMode: "disable",
	})
	if err != nil {
		t.Fatalf("buildPgxURL: %v", err)
	}
	if !strings.Contains(got, "p%40ss%2Fword") {
		t.Fatalf("password not URL-escaped: %q", got)
	}
}

func TestBuildPgxURL_OmitsPasswordWhenEmpty(t *testing.T) {
	got, err := buildPgxURL(Config{
		Host: "h", Port: 1, User: "u",
		Database: "d", SSLMode: "disable",
	})
	if err != nil {
		t.Fatalf("buildPgxURL: %v", err)
	}
	if strings.Contains(got, ":@") {
		t.Fatalf("expected no empty password segment, got %q", got)
	}
	if !strings.Contains(got, "u@h:1/d") {
		t.Fatalf("expected userinfo without password, got %q", got)
	}
}
