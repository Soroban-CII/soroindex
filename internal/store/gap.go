package store

import (
	"context"
	"fmt"
	"sort"
)

// GapRow is one contract in the undeclared gap.
type GapRow struct {
	ContractID string `json:"contract_id"`
	WasmHash   string `json:"wasm_hash"`
	OKCount    int    `json:"ok_count"`
}

// GapPage bounds undeclared results and allows continuation after the last ID.
type GapPage struct {
	Contracts  []GapRow `json:"contracts"`
	NextCursor *string  `json:"next_cursor"`
}

// GapGroup is one undeclared-gap Wasm and the live contracts that run it.
// Outreach works per code hash because the fix is one line in that code.
type GapGroup struct {
	WasmHash string `json:"wasm_hash"`
	OKCount  int    `json:"ok_count"`

	Contracts []string `json:"contracts"`
}

// GapGroups groups the undeclared gap for one SEP by Wasm hash, ordered by
// live contract count (largest first, hash as the tie-break) and capped at
// maxGroups. totalGroups and totalContracts describe the uncapped result, so
// a capped page can say what it left out. Archived contracts and contracts
// on archived code are excluded, like Gap.
func (s *Store) GapGroups(ctx context.Context, sep int, rulesetVersion string, maxGroups int) (groups []GapGroup, totalGroups, totalContracts int, err error) {
	groups = []GapGroup{}
	if sep < 1 || sep > 999999 || rulesetVersion == "" || maxGroups < 1 || maxGroups > 500 {
		return nil, 0, 0, ErrBadQuery
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT im.wasm_hash, im.ok_count, c.contract_id
FROM interface_matches im
JOIN contracts c ON c.current_wasm_hash = im.wasm_hash
WHERE im.sep = ? AND im.ruleset_version = ?
  AND im.status = 'match'
  AND c.archived = 0
  AND c.kind IN ('wasm','wasm_ref')
  AND NOT EXISTS (SELECT 1 FROM wasm_claims wc WHERE wc.wasm_hash = im.wasm_hash)
ORDER BY im.wasm_hash, c.contract_id`, sep, rulesetVersion)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("gap groups: %w", err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	byHash := map[string]int{}
	for rows.Next() {
		var hash, id string
		var ok int
		if err := rows.Scan(&hash, &ok, &id); err != nil {
			return nil, 0, 0, fmt.Errorf("gap groups: %w", err)
		}
		if _, seen := byHash[hash]; !seen {
			byHash[hash] = len(groups)
			groups = append(groups, GapGroup{WasmHash: hash, OKCount: ok})
		}
		g := &groups[byHash[hash]]
		g.Contracts = append(g.Contracts, id)
		totalContracts++
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("gap groups: %w", err)
	}
	totalGroups = len(groups)
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].Contracts) != len(groups[j].Contracts) {
			return len(groups[i].Contracts) > len(groups[j].Contracts)
		}
		return groups[i].WasmHash < groups[j].WasmHash
	})
	if len(groups) > maxGroups {
		groups = groups[:maxGroups]
	}
	return groups, totalGroups, totalContracts, nil
}

// Gap pages through the undeclared gap for one SEP (CLAUDE.md §5.6): live contracts
// whose current Wasm's inferred status for sep under rulesetVersion is
// "match" and that declare nothing at all. This is the inferred tier only;
// the contracts make no claim, and callers must label the result
// "inferred".
func (s *Store) Gap(ctx context.Context, sep int, rulesetVersion string, limit int, cursor string) (GapPage, error) {
	page := GapPage{Contracts: []GapRow{}}
	limit, after, err := contractPageInput(limit, cursor)
	if err != nil || sep < 1 || sep > 999999 || rulesetVersion == "" {
		return page, ErrBadQuery
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT c.contract_id, c.current_wasm_hash, im.ok_count
FROM contracts c
JOIN interface_matches im ON im.wasm_hash = c.current_wasm_hash AND im.sep = ? AND im.ruleset_version = ?
WHERE c.archived = 0
	AND c.contract_id > ?
  AND c.kind IN ('wasm','wasm_ref')
  AND im.status = 'match'
  AND NOT EXISTS (SELECT 1 FROM wasm_claims wc WHERE wc.wasm_hash = c.current_wasm_hash)
ORDER BY c.contract_id LIMIT ?`, sep, rulesetVersion, after, limit+1)
	if err != nil {
		return page, fmt.Errorf("gap: %w", err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	for rows.Next() {
		var r GapRow
		if err := rows.Scan(&r.ContractID, &r.WasmHash, &r.OKCount); err != nil {
			return page, fmt.Errorf("gap: %w", err)
		}
		page.Contracts = append(page.Contracts, r)
	}
	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("gap: %w", err)
	}
	if len(page.Contracts) > limit {
		page.Contracts = page.Contracts[:limit]
		cursor := encodeContractCursor(page.Contracts[limit-1].ContractID)
		page.NextCursor = &cursor
	}
	return page, nil
}
