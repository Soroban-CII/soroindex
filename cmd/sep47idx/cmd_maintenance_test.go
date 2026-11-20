package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestMaintenanceCLI(t *testing.T) {
	db, _, _ := readFixture(t)
	t.Setenv("SEP47IDX_DB", db)

	t.Run("text report lists each step, sizes and duration", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := run([]string{"maintenance"}, &out, &errb); code != 0 {
			t.Fatal(code, errb.String())
		}
		for _, want := range []string{"database: " + db, "integrity_check:", "ok (", "analyze:", "incremental_vacuum:", "skipped: auto_vacuum is none", "size before:", "size after:", "duration:"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("missing %q in:\n%s", want, out.String())
			}
		}
	})
	t.Run("json report", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := run([]string{"maintenance", "--json"}, &out, &errb); code != 0 {
			t.Fatal(code, errb.String())
		}
		var r store.MaintenanceReport
		if err := json.Unmarshal(out.Bytes(), &r); err != nil || !r.IntegrityOK || r.Before.PageCount == 0 || r.After.PageCount == 0 {
			t.Fatalf("%v\n%s", err, out.String())
		}
	})
	t.Run("a held write lock is refused with a clear message and exit 1", func(t *testing.T) {
		ctx := context.Background()
		s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		tx, err := s.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if err := tx.SetLastLedger(ctx, 11); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if code := run([]string{"maintenance"}, &out, &errb); code != 1 {
			t.Fatalf("exit %d, want 1; stderr %s", code, errb.String())
		}
		if !strings.Contains(errb.String(), "another process holds the database write lock (stop sync before running maintenance)") {
			t.Fatalf("stderr = %q", errb.String())
		}
	})
	t.Run("bad arguments exit 1", func(t *testing.T) {
		for _, args := range [][]string{{"maintenance", "extra"}, {"maintenance", "--timeout", "0s"}} {
			if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
				t.Fatal(args, code)
			}
		}
	})
	t.Run("an unwritable stdout is an error", func(t *testing.T) {
		if code := run([]string{"maintenance"}, failWriter{}, &bytes.Buffer{}); code != 1 {
			t.Fatal(code)
		}
	})
}
