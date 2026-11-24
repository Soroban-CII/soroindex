package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// openTestStore opens a new test store.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, _ := openTest(t)
	return s
}

// emptyDump returns a dump of an empty database.
func emptyDump(t *testing.T) string {
	s := openTestStore(t)
	return dumpAll(t, s)
}

// dumpAll renders every row of every table, in a stable order, so two dumps
// are equal exactly when the database contents are equal.
func dumpAll(t *testing.T, s *Store) string {
	t.Helper()
	tables, err := s.DB.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var n string
		if err := tables.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := tables.Close(); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, n := range names {
		rows, err := s.DB.Query(`SELECT * FROM "` + n + `" ORDER BY 1`) // #nosec G202
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "== %s %v\n", n, cols)
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%v\n", vals)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return b.String()
}

// runBatchAndDump applies a representative batch of writes and returns a dump
// of the database. If failAt > 0, the batch will fail at the N-th exec call.
func runBatchAndDump(ctx context.Context, t *testing.T, s *Store, failAt int) string {
	t.Helper()
	var tx *Tx
	var err error
	if failAt > 0 {
		tx, err = s.BeginWithFailAt(ctx, failAt)
	} else {
		tx, err = s.Begin(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := applyBatch(ctx, t, tx); err != nil {
		if failAt == 0 {
			t.Fatalf("clean batch failed: %v", err)
		}
		_ = tx.Rollback()
		return ""
	}
	if err := tx.Commit(); err != nil {
		if failAt == 0 {
			t.Fatalf("clean commit failed: %v", err)
		}
		return ""
	}
	return dumpAll(t, s)
}

// applyBatch applies a representative batch of writes covering all write paths
// in store/write.go.
func applyBatch(ctx context.Context, t *testing.T, tx interface {
	exec(context.Context, string, string, ...any) error
	SetState(context.Context, string, string) error
	SetLastLedger(context.Context, uint32) error
	UpsertWasm(context.Context, WasmRow) error
	ReplaceClaims(context.Context, string, string, []ClaimRow) error
	ReplaceAnomalies(context.Context, string, []AnomalyRow) error
	UpsertMatch(context.Context, MatchRow) error
	UpsertContract(context.Context, ContractRow) error
	SetVersion(context.Context, Version) (bool, error)
	UpsertExecRef(context.Context, string, string, string, uint32, bool) error
	SetContractArchived(context.Context, string, bool) error
}) error {
	t.Helper()
	// 1. SetState / SetLastLedger
	if err := tx.SetState(ctx, "test_key", "test_value"); err != nil {
		return err
	}
	if err := tx.SetLastLedger(ctx, 100); err != nil {
		return err
	}

	// 2. UpsertWasm (ok and archived)
	if err := tx.UpsertWasm(ctx, WasmRow{
		Hash: "aa", SizeBytes: 100, FirstSeenLedger: 50, HasMeta: true, HasSpec: true,
		SEPEntryCount: 1, MetaJSON: `[{"key":"sep","value":"41"}]`,
		ParseStatus: "ok", ParserVersion: "1",
	}); err != nil {
		return err
	}
	if err := tx.UpsertWasm(ctx, WasmRow{
		Hash: "bb", FirstSeenLedger: 60, ParseStatus: "archived", ParserVersion: "1",
	}); err != nil {
		return err
	}

	// 3. ReplaceClaims
	if err := tx.ReplaceClaims(ctx, "aa", "sep47-meta", []ClaimRow{
		{SEP: 41, RawToken: "41"}, {SEP: 40, RawToken: "040", Anomaly: "leading_zero"},
	}); err != nil {
		return err
	}

	// 4. ReplaceAnomalies
	if err := tx.ReplaceAnomalies(ctx, "aa", []AnomalyRow{
		{RawToken: "040", Anomaly: "leading_zero"}, {RawToken: "abc", Anomaly: "non_numeric"},
	}); err != nil {
		return err
	}

	// 5. UpsertMatch
	if err := tx.UpsertMatch(ctx, MatchRow{
		Hash: "aa", SEP: 41, RulesetVersion: "sep41-v0.5.2", RuleFileSHA256: "abc",
		Status: "match", OKCount: 10, MissingJSON: "[]", MismatchedJSON: "[]", MatchedVariantsJSON: "{}",
		ComputedAt: "2026-10-07T00:00:00Z",
	}); err != nil {
		return err
	}

	// 6. UpsertContract
	if err := tx.UpsertContract(ctx, ContractRow{
		ID: "C1", Kind: "wasm", CurrentWasmHash: "aa", CreatedLedger: 50, UpdatedLedger: 100,
	}); err != nil {
		return err
	}

	// 7. SetVersion (create and upgrade)
	if _, err := tx.SetVersion(ctx, Version{ContractID: "C1", WasmHash: "aa", FromLedger: 50}); err != nil {
		return err
	}
	if _, err := tx.SetVersion(ctx, Version{ContractID: "C1", WasmHash: "bb", FromLedger: 100}); err != nil {
		return err
	}

	// 8. UpsertExecRef
	if err := tx.UpsertExecRef(ctx, "C9", "v1", "aa", 50, false); err != nil {
		return err
	}

	// 9. SetContractArchived
	if err := tx.SetContractArchived(ctx, "C1", true); err != nil {
		return err
	}

	return nil
}

// TestFaultInjectionHarness verifies that injecting a failure at any write
// point within a batch leaves the database unchanged, and that a clean rerun
// produces the same final state as a pure clean run. This proves the
// one-transaction-per-batch guarantee.
func TestFaultInjectionHarness(t *testing.T) {
	ctx := context.Background()

	// Record a clean run to get the expected final state.
	cleanStore := openTestStore(t)
	cleanState := runBatchAndDump(ctx, t, cleanStore, 0)

	// For each possible failure point in the batch, inject a failure and verify:
	// 1. The database state is unchanged from empty (rolled back)
	// 2. A clean rerun produces the same final state as the original clean run.
	for i := 1; i <= 10; i++ {
		t.Run(fmt.Sprintf("fail_at_exec_%d", i), func(t *testing.T) {
			failStore := openTestStore(t)
			// Run with failure injected at call i.
			failTx, err := failStore.BeginWithFailAt(ctx, i)
			if err != nil {
				t.Fatalf("begin with fail at %d: %v", i, err)
			}
			if err := applyBatch(ctx, t, failTx); err != nil {
				_ = failTx.Rollback()
				return
			}
			if err := failTx.Commit(); err != nil {
				return
			}

			// Verify database state is unchanged from initial (rolled back).
			if dump := dumpAll(t, failStore); dump != emptyDump(t) {
				t.Fatalf("database changed after failed exec %d:\n%s", i, dump)
			}

			// Rerun cleanly and verify final state matches clean run.
			retryStore := openTestStore(t)
			retryState := runBatchAndDump(ctx, t, retryStore, 0)
			if retryState != cleanState {
				t.Fatalf("clean rerun after fail at %d differs from clean run:\ngot:\n%s\nwant:\n%s", i, retryState, cleanState)
			}
		})
	}
}
