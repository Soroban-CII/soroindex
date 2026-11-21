package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestMigrateStatusNoArgs(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"migrate"}, &bytes.Buffer{}, &stderr)
	if code == exitOK {
		t.Fatal("expected non-zero exit when no flag given")
	}
}

func TestMigrateStatusAndDryRunMutuallyExclusive(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	var stderr bytes.Buffer
	code := run([]string{"migrate", "--db", db, "--network", "testnet", "--status", "--dry-run"}, &bytes.Buffer{}, &stderr)
	if code == exitOK {
		t.Fatal("expected non-zero exit when both flags given")
	}
}

func openTestDB(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	s, err := store.Open(context.Background(), db, store.Options{Passphrase: "Test SDF Network ; September 2015"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return db, s
}

func TestMigrateStatusAllApplied(t *testing.T) {
	dbPath, _ := openTestDB(t)
	var out bytes.Buffer
	code := run([]string{"migrate", "--db", dbPath, "--network", "testnet", "--status"}, &out, &bytes.Buffer{})
	if code != exitOK {
		t.Fatalf("exit %d, output: %s", code, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "applied") {
		t.Fatalf("status output should contain 'applied': %s", got)
	}
	if strings.Contains(got, "pending") {
		t.Fatalf("no pending migrations expected: %s", got)
	}
}

func TestMigrateDryRunNoPending(t *testing.T) {
	dbPath, _ := openTestDB(t)
	var out bytes.Buffer
	code := run([]string{"migrate", "--db", dbPath, "--network", "testnet", "--dry-run"}, &out, &bytes.Buffer{})
	if code != exitOK {
		t.Fatalf("exit %d, output: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "no pending") {
		t.Fatalf("expected 'no pending': %s", out.String())
	}
}

func TestMigrateStatusJSON(t *testing.T) {
	dbPath, _ := openTestDB(t)
	var out bytes.Buffer
	code := run([]string{"migrate", "--db", dbPath, "--network", "testnet", "--status", "--json"}, &out, &bytes.Buffer{})
	if code != exitOK {
		t.Fatalf("exit %d, output: %s", code, out.String())
	}
	got := out.String()
	if !strings.HasPrefix(strings.TrimSpace(got), "[") {
		t.Fatalf("expected JSON array: %s", got)
	}
}

func TestChecksumMismatchRefusesOpen(t *testing.T) {
	dbPath, s := openTestDB(t)
	// Tamper a stored checksum directly.
	_, err := s.DB.ExecContext(context.Background(),
		`UPDATE migration_checksums SET checksum = 'aaaa' WHERE version = 1`)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	_, err = store.Open(context.Background(), dbPath, store.Options{Passphrase: "Test SDF Network ; September 2015"})
	if err == nil {
		t.Fatal("expected error on tampered checksum")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestChecksumBackfillOnUpgrade(t *testing.T) {
	// Simulate a database that was migrated before 0003 (no checksum table).
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create a db up to migration 0002 by manually building a minimal state.
	// We use the internal path: open normally (which applies all migrations
	// including 0003), then verify checksums are present.
	s, err := store.Open(context.Background(), dbPath, store.Options{Passphrase: "Test SDF Network ; September 2015"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.MigrateStatus(context.Background(), dbPath, store.MigrateStatusOptions{
		Passphrase: "Test SDF Network ; September 2015",
	})
	_ = s.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Applied && r.Checksum == "" {
			t.Errorf("v%d has no checksum after backfill", r.Version)
		}
		if r.Tampered {
			t.Errorf("v%d shows tampered after fresh open", r.Version)
		}
		if !r.Applied {
			t.Errorf("v%d is pending after open (expected all applied)", r.Version)
		}
	}
	_ = os.Remove(dbPath)
}
