package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseDSN_Basic(t *testing.T) {
	cfg, err := parseDSN("postgres://alice:secret@db.example.com:5433/orders?sslmode=require&application_name=test")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "db.example.com" || cfg.Port != 5433 {
		t.Errorf("host:port = %s:%d, want db.example.com:5433", cfg.Host, cfg.Port)
	}
	if cfg.User != "alice" || cfg.Password != "secret" {
		t.Errorf("creds = %s/%s", cfg.User, cfg.Password)
	}
	if cfg.Database != "orders" {
		t.Errorf("db = %s, want orders", cfg.Database)
	}
	if cfg.SSLMode != "require" {
		t.Errorf("sslmode = %s, want require", cfg.SSLMode)
	}
	if cfg.AppName != "test" {
		t.Errorf("appname = %s, want test", cfg.AppName)
	}
}

func TestParseDSN_DefaultPort5432(t *testing.T) {
	cfg, err := parseDSN("postgres://u@h/d")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 5432 {
		t.Errorf("port = %d, want 5432 default", cfg.Port)
	}
}

func TestParseDSN_BadScheme(t *testing.T) {
	if _, err := parseDSN("mysql://u@h/d"); err == nil {
		t.Error("expected error for non-postgres scheme")
	}
}

func TestParseExpiry_Days(t *testing.T) {
	exp, err := parseExpiry("30d")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Now().Add(30 * 24 * time.Hour)
	if d := want.Sub(exp); d > time.Second || d < -time.Second {
		t.Errorf("expiry off by %v, want within 1s", d)
	}
}

func TestParseExpiry_Duration(t *testing.T) {
	exp, err := parseExpiry("48h")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Now().Add(48 * time.Hour)
	if d := want.Sub(exp); d > time.Second || d < -time.Second {
		t.Errorf("expiry off by %v", d)
	}
}

func TestParseExpiry_Empty(t *testing.T) {
	exp, err := parseExpiry("")
	if err != nil {
		t.Fatal(err)
	}
	if !exp.IsZero() {
		t.Errorf("empty expiry = %v, want zero time", exp)
	}
}

func TestParseExpiry_Bad(t *testing.T) {
	if _, err := parseExpiry("not a duration"); err == nil {
		t.Error("expected error")
	}
}

func TestSplitCSV(t *testing.T) {
	cases := map[string][]string{
		"":        nil,
		"a":       {"a"},
		"a,b,c":   {"a", "b", "c"},
		" a , b ": {"a", "b"},
	}
	for in, want := range cases {
		got := splitCSV(in)
		if len(got) != len(want) {
			t.Errorf("splitCSV(%q) len = %d, want %d", in, len(got), len(want))
			continue
		}
		for i, w := range want {
			if got[i] != w {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", in, i, got[i], w)
			}
		}
	}
}

func TestIsHex(t *testing.T) {
	cases := map[string]bool{
		"":         false,
		"abc":      false, // odd length
		"deadbeef": true,
		"DEADBEEF": true,
		"xy":       false,
		"00ff":     true,
	}
	for in, want := range cases {
		if got := isHex(in); got != want {
			t.Errorf("isHex(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestGeneratePlainKey_PrefixAndLength(t *testing.T) {
	k, err := generatePlainKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k, "kit_") {
		t.Errorf("key = %q, missing kit_ prefix", k)
	}
	if len(k) < 40 {
		t.Errorf("key length = %d, want >= 40 chars (32 bytes b64 ≈ 43)", len(k))
	}
}

func TestFormatStatus_EmptyAndPopulated(t *testing.T) {
	if got := formatStatus(nil); !strings.Contains(got, "no migrations") {
		t.Errorf("empty formatStatus = %q", got)
	}
}
