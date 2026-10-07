package store

import (
	"context"
	"fmt"
)

// GapRow is one contract in the undeclared gap.
type GapRow struct {
	ContractID string `json:"contract_id"`
	WasmHash   string `json:"wasm_hash"`
	OKCount    int    `json:"ok_count"`
}

// Gap lists the undeclared gap for one SEP (CLAUDE.md §5.6): live contracts
// whose current Wasm's inferred status for sep under rulesetVersion is
// "match" and that declare nothing at all. This is the inferred tier only;
// the contracts make no claim, and callers must label the result
// "inferred".
func (s *Store) Gap(ctx context.Context, sep int, rulesetVersion string) ([]GapRow, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT c.contract_id, c.current_wasm_hash, im.ok_count
FROM contracts c
JOIN interface_matches im ON im.wasm_hash = c.current_wasm_hash AND im.sep = ? AND im.ruleset_version = ?
WHERE c.archived = 0
  AND c.kind IN ('wasm','wasm_ref')
  AND im.status = 'match'
  AND NOT EXISTS (SELECT 1 FROM wasm_claims wc WHERE wc.wasm_hash = c.current_wasm_hash)
ORDER BY c.contract_id`, sep, rulesetVersion)
	if err != nil {
		return nil, fmt.Errorf("gap: %w", err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	out := []GapRow{}
	for rows.Next() {
		var r GapRow
		if err := rows.Scan(&r.ContractID, &r.WasmHash, &r.OKCount); err != nil {
			return nil, fmt.Errorf("gap: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
