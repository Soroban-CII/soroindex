package store

import (
	"context"
	"testing"
)

// inTx runs f in a committed transaction.
func inTx(t *testing.T, s *Store, f func(*Tx) error) {
	t.Helper()
	tx, err := s.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := f(tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func queryString(t *testing.T, s *Store, q string, args ...any) string {
	t.Helper()
	var v *string
	if err := s.DB.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if v == nil {
		return "<nil>"
	}
	return *v
}

func TestUpsertWasmArchivedKeepsLastKnownData(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	inTx(t, s, func(tx *Tx) error {
		return tx.UpsertWasm(ctx, WasmRow{Hash: "aa", SizeBytes: 100, FirstSeenLedger: 50, HasMeta: true, HasSpec: true,
			SEPEntryCount: 1, MetaJSON: `[{"key":"sep","value":"41"}]`, ParseStatus: "ok", ParserVersion: "1"})
	})
	inTx(t, s, func(tx *Tx) error {
		return tx.UpsertWasm(ctx, WasmRow{Hash: "aa", FirstSeenLedger: 40, ParseStatus: "archived", ParserVersion: "1"})
	})
	got := queryString(t, s, `SELECT parse_status || '|' || size_bytes || '|' || has_meta || '|' || meta_json || '|' || first_seen_ledger FROM wasm WHERE hash = 'aa'`)
	if got != `ok|100|1|[{"key":"sep","value":"41"}]|40` {
		t.Fatalf("row = %s; archival must keep parsed data, and first_seen keeps the earliest", got)
	}
	inTx(t, s, func(tx *Tx) error {
		return tx.UpsertWasm(ctx, WasmRow{Hash: "bb", ParseStatus: "archived", ParserVersion: "1"})
	})
	if got := queryString(t, s, `SELECT parse_status FROM wasm WHERE hash = 'bb'`); got != "archived" {
		t.Fatalf("never-parsed archived wasm = %s", got)
	}
}

func TestReplaceClaims(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	inTx(t, s, func(tx *Tx) error {
		return tx.ReplaceClaims(ctx, "aa", "sep47-meta", []ClaimRow{{SEP: 41, RawToken: "41"}, {SEP: 40, RawToken: "040", Anomaly: "leading_zero"}})
	})
	inTx(t, s, func(tx *Tx) error { // replay: no change
		return tx.ReplaceClaims(ctx, "aa", "sep47-meta", []ClaimRow{{SEP: 41, RawToken: "41"}, {SEP: 40, RawToken: "040", Anomaly: "leading_zero"}})
	})
	if got := queryString(t, s, `SELECT group_concat(sep || ':' || raw_token || ':' || COALESCE(anomaly,''), ',') FROM (SELECT * FROM wasm_claims ORDER BY sep)`); got != "40:040:leading_zero,41:41:" {
		t.Fatalf("claims = %s", got)
	}
	inTx(t, s, func(tx *Tx) error { // a re-parse that no longer yields 40 removes it
		return tx.ReplaceClaims(ctx, "aa", "sep47-meta", []ClaimRow{{SEP: 41, RawToken: "41"}})
	})
	if got := queryString(t, s, `SELECT group_concat(sep) FROM wasm_claims`); got != "41" {
		t.Fatalf("claims = %s", got)
	}
	inTx(t, s, func(tx *Tx) error { return tx.ReplaceClaims(ctx, "aa", "other-source", nil) })
	if got := queryString(t, s, `SELECT group_concat(sep) FROM wasm_claims`); got != "41" {
		t.Fatalf("another source's empty set touched sep47-meta claims: %s", got)
	}
}

func TestSetVersion(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	set := func(v Version) bool {
		var changed bool
		inTx(t, s, func(tx *Tx) error {
			var err error
			changed, err = tx.SetVersion(ctx, v)
			return err
		})
		return changed
	}
	rows := func() string {
		return queryString(t, s, `SELECT group_concat(r, ' ') FROM (SELECT COALESCE(wasm_hash,'-') || '@' || from_ledger || '-' || COALESCE(to_ledger,'') AS r
			FROM contract_versions WHERE contract_id = 'C1' ORDER BY from_ledger)`)
	}
	if !set(Version{ContractID: "C1", WasmHash: "aa", FromLedger: 10}) || rows() != "aa@10-" {
		t.Fatalf("first version: %s", rows())
	}
	if set(Version{ContractID: "C1", WasmHash: "aa", FromLedger: 15}) || rows() != "aa@10-" {
		t.Fatalf("same code again must not open a version: %s", rows())
	}
	if !set(Version{ContractID: "C1", WasmHash: "bb", FromLedger: 20}) || rows() != "aa@10-20 bb@20-" {
		t.Fatalf("upgrade must close the old row and open one: %s", rows())
	}
	if set(Version{ContractID: "C1", WasmHash: "bb", FromLedger: 20}) || rows() != "aa@10-20 bb@20-" {
		t.Fatalf("replaying the upgrade ledger must change nothing: %s", rows())
	}
	if set(Version{ContractID: "C1", WasmHash: "aa", FromLedger: 12}) || rows() != "aa@10-20 bb@20-" {
		t.Fatalf("replaying an older ledger must not regress history: %s", rows())
	}
	if !set(Version{ContractID: "C1", ExecRefOwner: "C9", ExecRefTag: "v1", WasmHash: "bb", FromLedger: 30}) ||
		rows() != "aa@10-20 bb@20-30 bb@30-" {
		t.Fatalf("switching to a reference with the same hash still records the switch: %s", rows())
	}
}

func TestUpsertContractNeverRegresses(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	inTx(t, s, func(tx *Tx) error {
		return tx.UpsertContract(ctx, ContractRow{ID: "C1", Kind: "wasm", CurrentWasmHash: "bb", CreatedLedger: 10, UpdatedLedger: 20})
	})
	inTx(t, s, func(tx *Tx) error { // an older replay
		return tx.UpsertContract(ctx, ContractRow{ID: "C1", Kind: "wasm", CurrentWasmHash: "bb", CreatedLedger: 5, UpdatedLedger: 12})
	})
	if got := queryString(t, s, `SELECT created_ledger || '/' || updated_ledger FROM contracts WHERE contract_id = 'C1'`); got != "10/20" {
		t.Fatalf("created/updated = %s", got)
	}
	inTx(t, s, func(tx *Tx) error { return tx.SetContractArchived(ctx, "C1", true) })
	if got := queryString(t, s, `SELECT archived || current_wasm_hash FROM contracts WHERE contract_id = 'C1'`); got != "1bb" {
		t.Fatalf("archiving must touch only the flag: %s", got)
	}
}

func TestUnknownLedgersStayNull(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ { // the second pass is a replay
		inTx(t, s, func(tx *Tx) error {
			if err := tx.UpsertContract(ctx, ContractRow{ID: "C1", Kind: "wasm", CurrentWasmHash: "aa"}); err != nil {
				return err
			}
			return tx.UpsertExecRef(ctx, "C9", "v1", "aa", 0, false)
		})
	}
	if got := queryString(t, s, `SELECT COALESCE(updated_ledger, 'NULL') FROM contracts`); got != "NULL" {
		t.Fatalf("contracts.updated_ledger = %s, want NULL (unknown)", got)
	}
	if got := queryString(t, s, `SELECT COALESCE(updated_ledger, 'NULL') FROM exec_refs`); got != "NULL" {
		t.Fatalf("exec_refs.updated_ledger = %s, want NULL (unknown)", got)
	}
}

func TestRollbackLeavesNothing(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.SetLastLedger(ctx, 99); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.State(ctx, KeyLastLedger); err == nil {
		t.Fatal("rolled-back write is visible")
	}
}
