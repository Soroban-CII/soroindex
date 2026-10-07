package store

import (
	"context"
	"database/sql"
	"fmt"
)

// StaleWasm is a Wasm with no interface_matches row for a ruleset version.
type StaleWasm struct {
	Hash          string
	HasSpec       bool
	FunctionsJSON string // "" when NULL
	ParseStatus   string
}

// StaleForRuleset lists every Wasm lacking a match row for (sep,
// rulesetVersion), in hash order.
func (s *Store) StaleForRuleset(ctx context.Context, sep int, rulesetVersion string) ([]StaleWasm, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT w.hash, w.has_spec, w.functions_json, w.parse_status FROM wasm w
WHERE NOT EXISTS (SELECT 1 FROM interface_matches im WHERE im.wasm_hash = w.hash AND im.sep = ? AND im.ruleset_version = ?)
ORDER BY w.hash`, sep, rulesetVersion)
	if err != nil {
		return nil, fmt.Errorf("stale wasm: %w", err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	var out []StaleWasm
	for rows.Next() {
		var w StaleWasm
		var fns sql.NullString
		if err := rows.Scan(&w.Hash, &w.HasSpec, &fns, &w.ParseStatus); err != nil {
			return nil, fmt.Errorf("stale wasm: %w", err)
		}
		w.FunctionsJSON = fns.String
		out = append(out, w)
	}
	return out, rows.Err()
}
