package store

import (
	"context"
	"database/sql"
	"testing"
)

// invariantViolation is one raw SQL statement that the schema itself must
// reject (CLAUDE.md §5.8). setup runs first, in the same transaction, to
// create the row the violation collides with.
type invariantViolation struct {
	name  string
	setup []string
	sql   string
}

// invariantViolations lists every SQL-enforced invariant as a direct
// violation attempt. The upgrade harness runs them against databases that
// were migrated forward with data in them, so a migration that drops or
// weakens a trigger or CHECK fails a test, not a production write.
var invariantViolations = []invariantViolation{
	{
		name:  "a second current version for one contract",
		setup: []string{`INSERT INTO contract_versions (contract_id, wasm_hash, from_ledger, to_ledger) VALUES ('CINVARIANT', 'aa', 10, NULL)`},
		sql:   `INSERT INTO contract_versions (contract_id, wasm_hash, from_ledger, to_ledger) VALUES ('CINVARIANT', 'bb', 20, NULL)`,
	},
	{
		name: "reopening a closed version while another is current",
		setup: []string{
			`INSERT INTO contract_versions (contract_id, wasm_hash, from_ledger, to_ledger) VALUES ('CINVARIANT', 'aa', 10, 20)`,
			`INSERT INTO contract_versions (contract_id, wasm_hash, from_ledger, to_ledger) VALUES ('CINVARIANT', 'bb', 20, NULL)`,
		},
		sql: `UPDATE contract_versions SET to_ledger = NULL WHERE contract_id = 'CINVARIANT' AND from_ledger = 10`,
	},
	{
		name: "a failed verification marked passed",
		sql: `INSERT INTO verifications (contract_id, wasm_hash, sep, tool, tool_version, verdict, passed, clauses_json, run_at)
			VALUES ('CINVARIANT', 'aa', 41, 'soroban-guard', '0', 'fail', 1, '[]', '2026-01-01T00:00:00Z')`,
	},
	{
		name: "an unverifiable verification marked passed",
		sql: `INSERT INTO verifications (contract_id, wasm_hash, sep, tool, tool_version, verdict, passed, clauses_json, run_at)
			VALUES ('CINVARIANT', 'aa', 41, 'soroban-guard', '0', 'unverifiable', 1, '[]', '2026-01-01T00:00:00Z')`,
	},
	{name: "a wasm_ref without its reference", sql: `INSERT INTO contracts (contract_id, kind) VALUES ('CINVARIANT', 'wasm_ref')`},
	{name: "a wasm_ref with only an owner", sql: `INSERT INTO contracts (contract_id, kind, exec_ref_owner) VALUES ('CINVARIANT', 'wasm_ref', 'COWNER')`},
	{name: "a wasm contract naming a reference", sql: `INSERT INTO contracts (contract_id, kind, exec_ref_owner, exec_ref_tag) VALUES ('CINVARIANT', 'wasm', 'COWNER', 't')`},
	{name: "a SAC with a Wasm hash", sql: `INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('CINVARIANT', 'sac', 'aa')`},
}

// invariantDataChecks are queries that must count zero rows: the invariants
// restated over the data already present, because SQLite does not re-check
// existing rows when a migration rebuilds or alters a table.
var invariantDataChecks = []struct{ name, sql string }{
	{"contracts with more than one current version", `SELECT count(*) FROM (SELECT contract_id FROM contract_versions WHERE to_ledger IS NULL GROUP BY contract_id HAVING count(*) > 1)`},
	{"verifications passed without a pass verdict", `SELECT count(*) FROM verifications WHERE passed = 1 AND verdict <> 'pass'`},
	{"wasm_ref contracts missing a reference", `SELECT count(*) FROM contracts WHERE kind = 'wasm_ref' AND (exec_ref_owner IS NULL OR exec_ref_tag IS NULL)`},
	{"non-reference contracts carrying a reference", `SELECT count(*) FROM contracts WHERE kind <> 'wasm_ref' AND (exec_ref_owner IS NOT NULL OR exec_ref_tag IS NOT NULL)`},
	{"SACs with a Wasm hash", `SELECT count(*) FROM contracts WHERE kind = 'sac' AND current_wasm_hash IS NOT NULL`},
}

// assertInvariantsHold attempts every raw violation inside a transaction
// that is always rolled back, then checks the stored data and SQLite's own
// integrity and foreign-key checks.
func assertInvariantsHold(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	for _, v := range invariantViolations {
		func() {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }() // the probe must never persist
			for _, q := range v.setup {
				if _, err := tx.ExecContext(ctx, q); err != nil {
					t.Fatalf("%s: setup %q: %v", v.name, q, err)
				}
			}
			if _, err := tx.ExecContext(ctx, v.sql); err == nil {
				t.Errorf("invariant no longer enforced: %s was accepted", v.name)
			}
		}()
	}
	for _, c := range invariantDataChecks {
		var n int
		if err := db.QueryRowContext(ctx, c.sql).Scan(&n); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if n != 0 {
			t.Errorf("%s: %d rows", c.name, n)
		}
	}
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Errorf("integrity_check = %q, %v", integrity, err)
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Error("foreign_key_check reported violations")
	}
}

func TestInvariantViolationsAreRejectedOnFreshDatabase(t *testing.T) {
	s, _ := openTest(t)
	assertInvariantsHold(t, s.DB)
	// The probes ran in rolled-back transactions: nothing they wrote remains.
	for _, table := range []string{"contracts", "contract_versions", "verifications"} {
		var n int
		if err := s.DB.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE contract_id = 'CINVARIANT'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s kept %d probe rows (%v)", table, n, err)
		}
	}
}
