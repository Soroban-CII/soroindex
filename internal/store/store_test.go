package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

const testnet = "Test SDF Network ; September 2015"

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(context.Background(), p, Options{Passphrase: testnet})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, p
}

// exec runs raw SQL, bypassing every Go helper, as §5.8 requires for the
// invariant tests.
func exec(t *testing.T, s *Store, q string, args ...any) error {
	t.Helper()
	_, err := s.DB.ExecContext(context.Background(), q, args...)
	return err
}

func TestInvariantOneCurrentVersionRawSQL(t *testing.T) {
	s, _ := openTest(t)
	must := func(t *testing.T, q string) {
		t.Helper()
		if err := exec(t, s, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	must(t, `INSERT INTO contract_versions (contract_id, wasm_hash, from_ledger, to_ledger) VALUES ('C1', 'aa', 10, NULL)`)

	t.Run("a second open row for the same contract is rejected", func(t *testing.T) {
		err := exec(t, s, `INSERT INTO contract_versions (contract_id, wasm_hash, from_ledger, to_ledger) VALUES ('C1', 'bb', 20, NULL)`)
		if err == nil || !strings.Contains(err.Error(), "already has a current version") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("closing the old row then opening a new one is allowed", func(t *testing.T) {
		must(t, `UPDATE contract_versions SET to_ledger = 20 WHERE contract_id = 'C1' AND from_ledger = 10`)
		must(t, `INSERT INTO contract_versions (contract_id, wasm_hash, from_ledger, to_ledger) VALUES ('C1', 'bb', 20, NULL)`)
	})
	t.Run("reopening a closed row while another is open is rejected", func(t *testing.T) {
		err := exec(t, s, `UPDATE contract_versions SET to_ledger = NULL WHERE contract_id = 'C1' AND from_ledger = 10`)
		if err == nil || !strings.Contains(err.Error(), "already has a current version") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("another contract may have its own open row", func(t *testing.T) {
		must(t, `INSERT INTO contract_versions (contract_id, wasm_hash, from_ledger, to_ledger) VALUES ('C2', 'aa', 10, NULL)`)
	})
	t.Run("exactly one open row per contract remains", func(t *testing.T) {
		var n int
		if err := s.DB.QueryRow(`SELECT count(*) FROM contract_versions WHERE contract_id = 'C1' AND to_ledger IS NULL`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("open rows = %d, %v", n, err)
		}
	})
}

func TestInvariantOnlyAPassIsPassedRawSQL(t *testing.T) {
	s, _ := openTest(t)
	insert := func(verdict string, passed int, runAt string) error {
		return exec(t, s, `INSERT INTO verifications (contract_id, wasm_hash, sep, tool, tool_version, verdict, passed, clauses_json, run_at)
			VALUES ('C1', 'aa', 41, 'soroban-guard', '0.4.2', ?, ?, '[]', ?)`, verdict, passed, runAt)
	}
	tests := []struct {
		verdict string
		passed  int
		ok      bool
	}{
		{"pass", 1, true},
		{"pass", 0, true},
		{"fail", 0, true},
		{"unverifiable", 0, true},
		{"fail", 1, false},
		{"unverifiable", 1, false},
	}
	for i, tt := range tests {
		err := insert(tt.verdict, tt.passed, fmt.Sprintf("2026-10-07T00:00:%02dZ", i))
		if (err == nil) != tt.ok {
			t.Errorf("verdict %s passed %d: err = %v, want ok=%v", tt.verdict, tt.passed, err, tt.ok)
		}
	}
	t.Run("an update cannot turn a fail into passed", func(t *testing.T) {
		if err := exec(t, s, `UPDATE verifications SET passed = 1 WHERE verdict = 'fail'`); err == nil {
			t.Fatal("update accepted")
		}
	})
}

func TestInvariantContractKindsRawSQL(t *testing.T) {
	s, _ := openTest(t)
	tests := []struct {
		name string
		sql  string
		ok   bool
	}{
		{"wasm with a hash", `INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C1','wasm','aa')`, true},
		{"sac with an asset", `INSERT INTO contracts (contract_id, kind, sac_asset) VALUES ('C2','sac','native')`, true},
		{"wasm_ref naming its reference", `INSERT INTO contracts (contract_id, kind, exec_ref_owner, exec_ref_tag) VALUES ('C3','wasm_ref','C9','v1')`, true},
		{"wasm_ref without a reference", `INSERT INTO contracts (contract_id, kind) VALUES ('C4','wasm_ref')`, false},
		{"wasm_ref with only an owner", `INSERT INTO contracts (contract_id, kind, exec_ref_owner) VALUES ('C5','wasm_ref','C9')`, false},
		{"wasm carrying a reference", `INSERT INTO contracts (contract_id, kind, exec_ref_owner, exec_ref_tag) VALUES ('C6','wasm','C9','v1')`, false},
		{"sac with a wasm hash", `INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C7','sac','aa')`, false},
		{"wasm with a sac asset", `INSERT INTO contracts (contract_id, kind, sac_asset) VALUES ('C8','wasm','native')`, false},
		{"unknown kind", `INSERT INTO contracts (contract_id, kind) VALUES ('C10','other')`, false},
		{"archived must be 0 or 1", `INSERT INTO contracts (contract_id, kind, archived) VALUES ('C11','wasm',2)`, false},
		{"bad parse_status", `INSERT INTO wasm (hash, parse_status, parser_version) VALUES ('aa','weird','1')`, false},
		{"bad match status", `INSERT INTO interface_matches VALUES ('aa',41,'r','great',0,'[]','[]','{}','t')`, false},
		{"version closing before it opens", `INSERT INTO contract_versions (contract_id, from_ledger, to_ledger) VALUES ('C1', 10, 9)`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := exec(t, s, tt.sql); (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestOpen(t *testing.T) {
	ctx := context.Background()
	t.Run("a new database is migrated and records its network", func(t *testing.T) {
		s, _ := openTest(t)
		v, err := s.State(ctx, KeySchemaVersion)
		want, _ := SchemaVersion()
		if err != nil || v != "1" || want != 1 {
			t.Fatalf("schema_version = %q, %v (binary knows %d)", v, err, want)
		}
		if p, err := s.State(ctx, KeyNetworkPassphrase); err != nil || p != testnet {
			t.Fatalf("passphrase = %q, %v", p, err)
		}
	})
	t.Run("reopening is idempotent", func(t *testing.T) {
		s, p := openTest(t)
		_ = s.Close()
		s2, err := Open(ctx, p, Options{Passphrase: testnet})
		if err != nil {
			t.Fatal(err)
		}
		_ = s2.Close()
	})
	t.Run("another network is refused", func(t *testing.T) {
		s, p := openTest(t)
		_ = s.Close()
		_, err := Open(ctx, p, Options{Passphrase: "Public Global Stellar Network ; September 2015"})
		if !errors.Is(err, ErrNetworkMismatch) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a schema newer than the binary is refused", func(t *testing.T) {
		s, p := openTest(t)
		if err := exec(t, s, `UPDATE sync_state SET value = '99' WHERE key = 'schema_version'`); err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		_, err := Open(ctx, p, Options{Passphrase: testnet})
		if !errors.Is(err, ErrSchemaTooNew) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("read-only open cannot write", func(t *testing.T) {
		s, p := openTest(t)
		_ = s.Close()
		ro, err := Open(ctx, p, Options{Passphrase: testnet, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ro.Close() }()
		if err := ro.SetState(ctx, "x", "y"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "readonly") {
			t.Fatalf("write on read-only store: err = %v", err)
		}
	})
	t.Run("read-only open of a missing database fails without creating it", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "absent.db")
		if _, err := Open(ctx, p, Options{Passphrase: testnet, ReadOnly: true}); err == nil {
			t.Fatal("want error")
		}
		if _, err := Open(ctx, p, Options{Passphrase: testnet, ReadOnly: true}); err == nil {
			t.Fatal("second read-only open succeeded: the first must not have created the file")
		}
	})
	t.Run("passphrase is required", func(t *testing.T) {
		if _, err := Open(ctx, filepath.Join(t.TempDir(), "x.db"), Options{}); err == nil {
			t.Fatal("want error")
		}
	})
}
