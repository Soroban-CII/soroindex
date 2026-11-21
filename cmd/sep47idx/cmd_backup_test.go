package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupCLI(t *testing.T) {
	db, _, _ := readFixture(t)

	t.Run("creates snapshot at --out path", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "snap.db")
		t.Setenv("SEP47IDX_DB", db)
		var out, errb bytes.Buffer
		if code := run([]string{"backup", "--out", dst}, &out, &errb); code != 0 {
			t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
		}
		if !strings.Contains(out.String(), dst) {
			t.Fatalf("stdout missing destination path: %s", out.String())
		}
		if _, err := os.Stat(dst); err != nil {
			t.Fatalf("snapshot file missing: %v", err)
		}
	})

	t.Run("refuses to overwrite an existing destination", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "existing.db")
		t.Setenv("SEP47IDX_DB", db)
		var out, errb bytes.Buffer
		if code := run([]string{"backup", "--out", dst}, &out, &errb); code != 0 {
			t.Fatal(code, errb.String())
		}
		// Second backup to the same path must fail.
		errb.Reset()
		if code := run([]string{"backup", "--out", dst}, &out, &errb); code == 0 {
			t.Fatal("expected exit 1 on overwrite, got 0")
		}
		if !strings.Contains(errb.String(), "backup") {
			t.Fatalf("stderr missing 'backup': %s", errb.String())
		}
	})

	t.Run("missing --out fails with a clear message", func(t *testing.T) {
		t.Setenv("SEP47IDX_DB", db)
		var out, errb bytes.Buffer
		if code := run([]string{"backup"}, &out, &errb); code == 0 {
			t.Fatal("expected exit 1")
		}
		if !strings.Contains(errb.String(), "--out") {
			t.Fatalf("stderr missing '--out': %s", errb.String())
		}
	})
}
