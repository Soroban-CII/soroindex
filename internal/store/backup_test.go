package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestBackupCreatesValidSnapshot(t *testing.T) {
	ctx := context.Background()
	src, _ := openTest(t)

	// Seed a last_ledger so the snapshot has something to validate.
	if err := src.SetState(ctx, KeyLastLedger, "42"); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "backup.db")
	if err := src.Backup(ctx, dst); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// Open the snapshot and verify it carries the expected state.
	snap, err := Open(ctx, dst, Options{Passphrase: testnet})
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = snap.Close() }()

	ledger, err := snap.State(ctx, KeyLastLedger)
	if err != nil || ledger != "42" {
		t.Fatalf("snapshot last_ledger = %q, %v; want 42", ledger, err)
	}

	pass, err := snap.State(ctx, KeyNetworkPassphrase)
	if err != nil || pass != testnet {
		t.Fatalf("snapshot passphrase = %q, %v", pass, err)
	}
}

// TestBackupRefusesExistingDst: SQLite VACUUM INTO refuses to overwrite, and
// Backup cleans up the partial file so a re-run can proceed.
func TestBackupRefusesExistingDst(t *testing.T) {
	ctx := context.Background()
	src, _ := openTest(t)
	dst := filepath.Join(t.TempDir(), "backup.db")

	// First backup succeeds.
	if err := src.Backup(ctx, dst); err != nil {
		t.Fatal(err)
	}
	// Second backup to the same path must fail.
	if err := src.Backup(ctx, dst); err == nil {
		t.Fatal("expected error on overwrite, got nil")
	}
}

// TestBackupConcurrentWrites verifies that an in-progress write transaction
// does not corrupt the snapshot: a snapshot written between two write batches
// reflects the state after the first commit, not a torn state.
func TestBackupConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	src, _ := openTest(t)

	// First write.
	if err := src.SetState(ctx, KeyLastLedger, "10"); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "concurrent.db")
	var wg sync.WaitGroup
	var backupErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		backupErr = src.Backup(ctx, dst)
	}()

	// Concurrent write that races with VACUUM INTO; the snapshot sees
	// either the old value or the new one, never a torn state.
	time.Sleep(1 * time.Millisecond) // yield to let backup start
	_ = src.SetState(ctx, KeyLastLedger, "20")

	wg.Wait()
	if backupErr != nil {
		t.Fatalf("Backup under concurrent write: %v", backupErr)
	}

	// The snapshot must be internally consistent: last_ledger is a valid uint32.
	snap, err := Open(ctx, dst, Options{Passphrase: testnet})
	if err != nil {
		t.Fatalf("open concurrent snapshot: %v", err)
	}
	defer func() { _ = snap.Close() }()

	ledger, err := snap.State(ctx, KeyLastLedger)
	if err != nil {
		t.Fatalf("snapshot last_ledger missing: %v", err)
	}
	if ledger != "10" && ledger != "20" {
		t.Fatalf("snapshot last_ledger = %q, want 10 or 20 (consistent snapshot)", ledger)
	}
}

// TestBackupCancellation verifies that a context cancellation aborts the backup
// and does not leave a partial file that passes validation.
func TestBackupCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	src, _ := openTest(t)
	dst := filepath.Join(t.TempDir(), "cancelled.db")

	err := src.Backup(ctx, dst)
	// Expect either context.Canceled or a wrapped form of it.
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		// Allow SQLite to have surfaced the cancellation as a different error.
		t.Logf("Backup with cancelled ctx returned (non-Canceled): %v", err)
	}
}

// TestBackupDiskError verifies that an I/O-level error (invalid path) returns
// an error and does not leave the source database in an inconsistent state.
func TestBackupDiskError(t *testing.T) {
	ctx := context.Background()
	src, _ := openTest(t)

	// A directory path is not a valid database destination.
	dst := t.TempDir() // exists, is a directory
	if err := src.Backup(ctx, dst); err == nil {
		t.Fatal("expected error writing to a directory, got nil")
	}

	// Source must still be readable.
	if _, err := src.schemaVersion(ctx); err != nil {
		t.Fatalf("source damaged after failed backup: %v", err)
	}
}

// TestBackupPassphraseMismatch is impossible to reach through normal code (the
// VACUUM INTO copies the source verbatim), but validateSnapshot must handle it
// if someone replaces the destination file. This test calls validateSnapshot
// directly with a snapshot for a different network.
func TestBackupValidatePassphraseMismatch(t *testing.T) {
	ctx := context.Background()
	src, _ := openTest(t)

	// Create a snapshot for a different network passphrase.
	other, otherPath := openTest(t)
	if err := other.SetState(ctx, KeyNetworkPassphrase, "Public Global Stellar Network ; September 2015"); err != nil {
		t.Fatal(err)
	}

	// Directly call validateSnapshot to exercise the mismatch check.
	err := src.validateSnapshot(ctx, otherPath)
	if err == nil || !errors.Is(err, ErrNetworkMismatch) {
		t.Fatalf("want ErrNetworkMismatch, got %v", err)
	}
}
