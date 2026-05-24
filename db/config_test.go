package db

import (
	"strings"
	"testing"
	"time"
)

func TestBuildConnString_MinimumRequiredFields(t *testing.T) {
	got := buildConnString(Config{
		Host: "db.internal", Port: 5432,
		User: "alice", Password: "s3cret",
		Database: "app", SSLMode: "disable",
	})
	want := "postgres://alice:s3cret@db.internal:5432/app?sslmode=disable"
	if got != want {
		t.Fatalf("buildConnString = %q, want %q", got, want)
	}
}

func TestBuildConnString_IncludesAppNameAndTimeoutWhenSet(t *testing.T) {
	got := buildConnString(Config{
		Host: "h", Port: 1, User: "u", Database: "d",
		SSLMode: "require", AppName: "checkout-api",
		ConnectTimeout: 7 * time.Second,
	})
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

func TestBuildConnString_EscapesPasswordSpecialChars(t *testing.T) {
	got := buildConnString(Config{
		Host: "h", Port: 1, User: "u", Password: "p@ss/word",
		Database: "d", SSLMode: "disable",
	})
	if !strings.Contains(got, "p%40ss%2Fword") {
		t.Fatalf("password not URL-escaped: %q", got)
	}
}

func TestBuildConnString_OmitsPasswordWhenEmpty(t *testing.T) {
	got := buildConnString(Config{
		Host: "h", Port: 1, User: "u",
		Database: "d", SSLMode: "disable",
	})
	if strings.Contains(got, ":@") {
		t.Fatalf("expected no empty password segment, got %q", got)
	}
	if !strings.Contains(got, "u@h:1/d") {
		t.Fatalf("expected userinfo without password, got %q", got)
	}
}
