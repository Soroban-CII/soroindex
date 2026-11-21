package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// maintenanceFixture builds a small deterministic index (the plan-test
// fixture's builder at a tiny size) and returns its closed path. The
// builder runs ANALYZE, so the stat table is dropped again here: tests can
// then see whether maintenance refreshed statistics.
func maintenanceFixture(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "index.db")
	if err := (catalogFixture{Hashes: 300, Contracts: 300}).build(ctx, p); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, p, Options{Passphrase: testnet})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec(t, s, `DROP TABLE sqlite_stat1`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func hasStatTable(t *testing.T, p string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+p+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'sqlite_stat1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func stepNames(r MaintenanceReport) string {
	var names []string
	for _, s := range r.Steps {
		names = append(names, s.Name)
	}
	return strings.Join(names, ",")
}

func TestMaintainOnFixtureDatabase(t *testing.T) {
	ctx := context.Background()
	p := maintenanceFixture(t)
	r, err := Maintain(ctx, p, MaintenanceOptions{Passphrase: testnet})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("integrity is checked and reported ok", func(t *testing.T) {
		if !r.IntegrityOK || len(r.Integrity) != 1 || r.Integrity[0] != "ok" {
			t.Fatalf("integrity = %v %v", r.IntegrityOK, r.Integrity)
		}
	})
	t.Run("statistics are refreshed", func(t *testing.T) {
		if !hasStatTable(t, p) {
			t.Fatal("ANALYZE did not run: no sqlite_stat1")
		}
	})
	t.Run("every step is listed in order with a duration", func(t *testing.T) {
		if got := stepNames(r); got != "lock,integrity_check,analyze,commit,incremental_vacuum,wal_checkpoint" {
			t.Fatalf("steps = %s", got)
		}
		for _, s := range r.Steps {
			if s.DurationMS < 0 || s.Result == "" {
				t.Fatalf("step %+v", s)
			}
		}
	})
	t.Run("a database without auto_vacuum says why vacuum was skipped", func(t *testing.T) {
		if r.AutoVacuum != "none" || !strings.Contains(r.Steps[4].Result, "--enable-incremental-vacuum") {
			t.Fatalf("auto_vacuum %s, step %+v", r.AutoVacuum, r.Steps[4])
		}
	})
	t.Run("sizes before and after are reported", func(t *testing.T) {
		if r.Before.PageCount == 0 || r.Before.Bytes != r.Before.PageSize*r.Before.PageCount || r.After.FileBytes == 0 {
			t.Fatalf("before %+v after %+v", r.Before, r.After)
		}
	})
	t.Run("the database still opens and holds its data", func(t *testing.T) {
		s, err := Open(ctx, p, Options{Passphrase: testnet, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		var n int
		if err := s.DB.QueryRow(`SELECT count(*) FROM wasm_claims`).Scan(&n); err != nil || n != 3000 {
			t.Fatalf("claims = %d, %v", n, err)
		}
	})
}

// freePages deletes rows so the file has free pages to reclaim.
func freePages(t *testing.T, p string) {
	t.Helper()
	s, err := Open(context.Background(), p, Options{Passphrase: testnet})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := exec(t, s, `DELETE FROM wasm_claims WHERE sep % 2 = 0`); err != nil {
		t.Fatal(err)
	}
}

func TestMaintainIncrementalVacuum(t *testing.T) {
	ctx := context.Background()
	p := maintenanceFixture(t)
	freePages(t, p)

	first, err := Maintain(ctx, p, MaintenanceOptions{Passphrase: testnet, EnableIncrementalVacuum: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("enabling incremental vacuum reclaims free pages once", func(t *testing.T) {
		if first.Before.FreelistCount == 0 || first.AutoVacuum != "incremental" || first.After.PageCount >= first.Before.PageCount || first.After.FreelistCount != 0 {
			t.Fatalf("before %+v after %+v auto_vacuum %s", first.Before, first.After, first.AutoVacuum)
		}
		if !strings.Contains(stepNames(first), "enable_incremental_vacuum") {
			t.Fatalf("steps = %s", stepNames(first))
		}
	})

	s, err := Open(ctx, p, Options{Passphrase: testnet})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec(t, s, `DELETE FROM wasm_claims`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	second, err := Maintain(ctx, p, MaintenanceOptions{Passphrase: testnet})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("later runs vacuum incrementally inside the locked transaction", func(t *testing.T) {
		if got := stepNames(second); got != "lock,integrity_check,analyze,incremental_vacuum,commit,wal_checkpoint" {
			t.Fatalf("steps = %s", got)
		}
		if second.Before.FreelistCount == 0 || second.FreedPages != second.Before.FreelistCount || second.After.FreelistCount != 0 {
			t.Fatalf("before %+v after %+v freed %d", second.Before, second.After, second.FreedPages)
		}
	})
}

func TestMaintainRefusesWhileWriterHoldsLock(t *testing.T) {
	ctx := context.Background()
	p := maintenanceFixture(t)
	writer, err := Open(ctx, p, Options{Passphrase: testnet})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	tx, err := writer.Begin(ctx) // what sync holds while it writes a batch
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.SetState(ctx, KeyLastLedger, "201"); err != nil {
		t.Fatal(err)
	}

	r, err := Maintain(ctx, p, MaintenanceOptions{Passphrase: testnet})
	if !errors.Is(err, ErrWriterActive) {
		t.Fatalf("err = %v, want ErrWriterActive", err)
	}
	if !strings.Contains(err.Error(), "stop sync") {
		t.Fatalf("message does not tell the operator what to do: %v", err)
	}
	if strings.Contains(stepNames(r), "integrity_check") {
		t.Fatalf("ran steps without the lock: %s", stepNames(r))
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("the writer's batch must be unaffected: %v", err)
	}
	if hasStatTable(t, p) {
		t.Fatal("ANALYZE ran while another process held the lock")
	}
	if _, err := Maintain(ctx, p, MaintenanceOptions{Passphrase: testnet}); err != nil {
		t.Fatalf("after the writer committed: %v", err)
	}
}

// corruptIndex flips one character of a stored Wasm hash inside the first
// index leaf page that holds it. The table row still holds the original
// hash, so index and table disagree: a corruption integrity_check must
// report, but one that leaves the file openable.
func corruptIndex(t *testing.T, p string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	var pageSize int64
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p) // #nosec G304 -- test temp file
	if err != nil {
		t.Fatal(err)
	}
	target := []byte(fixtureHash(4))
	for page := int64(0); page*pageSize < int64(len(b)); page++ {
		pg := b[page*pageSize : (page+1)*pageSize]
		// 0x0a is an index leaf page; skip table pages so only the index changes.
		hdr := 0
		if page == 0 {
			hdr = 100
		}
		if pg[hdr] != 0x0a {
			continue
		}
		if i := bytes.Index(pg, target); i >= 0 {
			pg[i+63] = 'f'                                    // ...0004 becomes ...000f
			if err := os.WriteFile(p, b, 0o600); err != nil { // #nosec G703 -- p is the test's own temp file
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("no index leaf page holds %s", target)
}

func TestMaintainReportsCorruptedCopy(t *testing.T) {
	ctx := context.Background()
	p := maintenanceFixture(t)
	corruptIndex(t, p)
	before, err := os.ReadFile(p) // #nosec G304 -- test temp file
	if err != nil {
		t.Fatal(err)
	}

	r, err := Maintain(ctx, p, MaintenanceOptions{Passphrase: testnet, EnableIncrementalVacuum: true})
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("err = %v, want ErrIntegrity", err)
	}
	t.Run("the problems are reported", func(t *testing.T) {
		if r.IntegrityOK || len(r.Integrity) == 0 || r.Integrity[0] == "ok" {
			t.Fatalf("integrity = %v %v", r.IntegrityOK, r.Integrity)
		}
		if !strings.Contains(strings.Join(r.Integrity, "\n"), "missing from index") {
			t.Fatalf("report does not describe the index damage: %v", r.Integrity)
		}
	})
	t.Run("nothing else runs on a damaged file", func(t *testing.T) {
		if got := stepNames(r); got != "lock,integrity_check" {
			t.Fatalf("steps = %s", got)
		}
		if hasStatTable(t, p) {
			t.Fatal("ANALYZE ran on a corrupted database")
		}
		after, err := os.ReadFile(p) // #nosec G304 -- test temp file
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("the corrupted file was modified; it must stay as found for diagnosis")
		}
	})
}

func TestMaintainRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("a missing database is not created", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "absent.db")
		if _, err := Maintain(ctx, p, MaintenanceOptions{Passphrase: testnet}); err == nil {
			t.Fatal("want error")
		}
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("maintenance created %s", p)
		}
	})
	t.Run("another network is refused", func(t *testing.T) {
		p := maintenanceFixture(t)
		_, err := Maintain(ctx, p, MaintenanceOptions{Passphrase: "Public Global Stellar Network ; September 2015"})
		if !errors.Is(err, ErrNetworkMismatch) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an older schema is refused, not migrated", func(t *testing.T) {
		ms, err := loadMigrations()
		if err != nil {
			t.Fatal(err)
		}
		p, db := buildAtVersion(t, ms, 1)
		_ = db.Close()
		_, err = Maintain(ctx, p, MaintenanceOptions{Passphrase: testnet})
		if !errors.Is(err, ErrSchemaTooOld) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a passphrase is required", func(t *testing.T) {
		if _, err := Maintain(ctx, maintenanceFixture(t), MaintenanceOptions{}); err == nil {
			t.Fatal("want error")
		}
	})
}
