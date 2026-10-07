package store

import (
	"context"
	"fmt"
)

// Totals are the adoption numbers recomputed from stored rows, with the
// same definitions phase0 uses, so `phase0 --verify-db` can show that the
// index stores what the census measured (CLAUDE.md §7 step 20).
//
// "Measured" contracts are live Wasm-running contracts (kind wasm or a
// resolved wasm_ref) whose current Wasm is parsed, not archived. "Used"
// hashes are the distinct current hashes of measured contracts.
type Totals struct {
	LiveSAC, LiveWasm, LiveWasmRef int
	WasmRefUnresolved              int
	MeasuredContracts              int
	UsedHashes                     int
	DeclaringContracts             int
	DeclaringUsedHashes            int
	SEP41ByContract                map[string]int // status -> measured contracts
	SEP41ByUsedHash                map[string]int // status -> used hashes
	GapContracts                   int            // SEP-41 match and declares nothing
	GapUsedHashes                  int
}

// Totals computes the numbers for one ruleset version of SEP-41.
func (s *Store) Totals(ctx context.Context, sep41Ruleset string) (Totals, error) {
	t := Totals{SEP41ByContract: map[string]int{}, SEP41ByUsedHash: map[string]int{}}
	one := func(dst *int, q string, args ...any) error {
		if err := s.DB.QueryRowContext(ctx, q, args...).Scan(dst); err != nil {
			return fmt.Errorf("totals: %w", err)
		}
		return nil
	}
	const measured = `SELECT c.contract_id, c.current_wasm_hash AS h FROM contracts c JOIN wasm w ON w.hash = c.current_wasm_hash
		WHERE c.archived = 0 AND c.kind IN ('wasm','wasm_ref') AND w.parse_status <> 'archived'`
	const declares = `EXISTS (SELECT 1 FROM wasm_claims wc WHERE wc.wasm_hash = m.h)`
	steps := []struct {
		dst  *int
		q    string
		args []any
	}{
		{&t.LiveSAC, `SELECT count(*) FROM contracts WHERE archived = 0 AND kind = 'sac'`, nil},
		{&t.LiveWasm, `SELECT count(*) FROM contracts WHERE archived = 0 AND kind = 'wasm'`, nil},
		{&t.LiveWasmRef, `SELECT count(*) FROM contracts WHERE archived = 0 AND kind = 'wasm_ref'`, nil},
		{&t.WasmRefUnresolved, `SELECT count(*) FROM contracts WHERE archived = 0 AND kind = 'wasm_ref' AND current_wasm_hash IS NULL`, nil},
		{&t.MeasuredContracts, `SELECT count(*) FROM (` + measured + `)`, nil},
		{&t.UsedHashes, `SELECT count(DISTINCT h) FROM (` + measured + `)`, nil},
		{&t.DeclaringContracts, `SELECT count(*) FROM (` + measured + `) m WHERE ` + declares, nil},
		{&t.DeclaringUsedHashes, `SELECT count(DISTINCT h) FROM (` + measured + `) m WHERE ` + declares, nil},
		{&t.GapContracts, `SELECT count(*) FROM (` + measured + `) m JOIN interface_matches im ON im.wasm_hash = m.h
			WHERE im.sep = 41 AND im.ruleset_version = ? AND im.status = 'match' AND NOT ` + declares, []any{sep41Ruleset}},
		{&t.GapUsedHashes, `SELECT count(DISTINCT m.h) FROM (` + measured + `) m JOIN interface_matches im ON im.wasm_hash = m.h
			WHERE im.sep = 41 AND im.ruleset_version = ? AND im.status = 'match' AND NOT ` + declares, []any{sep41Ruleset}},
	}
	for _, st := range steps {
		if err := one(st.dst, st.q, st.args...); err != nil {
			return Totals{}, err
		}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT im.status, count(*), count(DISTINCT m.h) FROM (`+measured+`) m
		JOIN interface_matches im ON im.wasm_hash = m.h WHERE im.sep = 41 AND im.ruleset_version = ? GROUP BY im.status`, sep41Ruleset)
	if err != nil {
		return Totals{}, fmt.Errorf("totals: %w", err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	for rows.Next() {
		var status string
		var byContract, byHash int
		if err := rows.Scan(&status, &byContract, &byHash); err != nil {
			return Totals{}, err
		}
		t.SEP41ByContract[status], t.SEP41ByUsedHash[status] = byContract, byHash
	}
	return t, rows.Err()
}
