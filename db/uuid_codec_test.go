package db_test

import (
	"context"
	"strings"
	"testing"
)

// TestUUIDBytes_RoundTrip regression-guards v1.0.1 P1-8 (LicenseKit
// followup): a raw [16]byte must pass through pgx's encoder for a
// uuid column without an intermediate pgtype.UUID wrap, and decode
// back into a [16]byte target symmetrically. Pre-fix this fails with
// "unable to encode 0x.. into binary format for uuid (OID 2950):
// cannot find encode plan" because pgx's TypeMap had no
// default-pg-type mapping for [16]byte.
func TestUUIDBytes_RoundTrip(t *testing.T) {
	d := startTestDB(t)
	ctx := context.Background()

	if _, err := d.Exec(ctx, `CREATE TABLE u (id uuid PRIMARY KEY)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	in := [16]byte{
		0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
		0x10, 0x32, 0x54, 0x76, 0x98, 0xba, 0xdc, 0xfe,
	}
	if _, err := d.Exec(ctx, `INSERT INTO u (id) VALUES ($1)`, in); err != nil {
		t.Fatalf("insert [16]byte arg: %v", err)
	}

	var out [16]byte
	if err := d.QueryRow(ctx, `SELECT id FROM u`).Scan(&out); err != nil {
		t.Fatalf("scan into [16]byte: %v", err)
	}
	if out != in {
		t.Errorf("round-trip = %x, want %x", out, in)
	}

	// Query by [16]byte arg also resolves to the right row.
	var matched [16]byte
	if err := d.QueryRow(ctx, `SELECT id FROM u WHERE id = $1`, in).Scan(&matched); err != nil {
		t.Fatalf("select by [16]byte arg: %v", err)
	}
	if matched != in {
		t.Errorf("select-by-uuid = %x, want %x", matched, in)
	}
}

// TestUUIDBytes_NoConnInit_StillRegistered verifies the codec is
// installed even when the caller passes neither WithConnInit nor
// WithDefaultStatementTimeout. Pre-fix, composeAfterConnect returned
// nil in that case and the codec never got a chance to register.
func TestUUIDBytes_NoConnInit_StillRegistered(t *testing.T) {
	// startTestDB constructs the DB with no extra options, exercising
	// exactly the "no statement_timeout, no connInit" path. If the
	// round-trip works here, the codec ran on the only hook entry the
	// kit installs unconditionally.
	d := startTestDB(t)
	ctx := context.Background()

	if _, err := d.Exec(ctx, `CREATE TABLE u_default (id uuid PRIMARY KEY)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	id := [16]byte{
		0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11,
		0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99,
	}
	if _, err := d.Exec(ctx, `INSERT INTO u_default (id) VALUES ($1)`, id); err != nil {
		// Without the unconditional UUID codec registration, this
		// returns "unable to encode … for uuid". Surface a clearer
		// failure message than the raw pgx text.
		if strings.Contains(err.Error(), "encode") && strings.Contains(err.Error(), "uuid") {
			t.Fatalf("UUID codec not installed on default conn — encoder absent: %v", err)
		}
		t.Fatalf("insert: %v", err)
	}
}
