package ingest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
)

// RecomputeStats summarizes a --recompute run.
type RecomputeStats struct {
	Recomputed   int // matched again from functions_json
	NoSpec       int // parsed, no spec: recorded as no_spec
	NeedsRefetch int // has a spec but no functions_json (analyzed before migration 0002)
	NeverParsed  int // archived before it could ever be parsed: nothing to match
	HashChanged  int // rule file SHA-256 changed, stale match
}

// Recompute re-runs the matcher for every Wasm that has no match row under
// the loaded ruleset versions, from stored functions_json, without fetching
// anything (CLAUDE.md §5.9 step 6). Rows for older ruleset versions are
// left in place as history. Each rule file is one transaction.
// Additionally, --recompute treats a rule file SHA-256 change as stale even
// if the ruleset_version label is unchanged (CLAUDE.md §5.9, amended 2026-10-07).
func (ix *Indexer) Recompute(ctx context.Context) (RecomputeStats, error) {
	var st RecomputeStats
	seen := map[string]bool{}
	for _, rf := range ix.Rules {
		var stale2 []store.StaleWasm

		// Find Wasm that lack a match row for the current ruleset version.
		stale, err := ix.Store.StaleForRuleset(ctx, rf.SEP, rf.RulesetVersion)
		if err != nil {
			return st, err
		}
		for _, w := range stale {
			if seen[w.Hash] {
				continue
			}
			seen[w.Hash] = true
			stale2 = append(stale2, w)
		}

		// Also find Wasm whose rule file SHA-256 has changed (or is unknown).
		// Compute the current rule file's SHA-256 and compare with stored values.
		hashChanged, err := ix.findWasmWithHashChange(ctx, rf.SEP, rf.RulesetVersion)
		if err != nil {
			return st, err
		}
		for _, w := range hashChanged {
			if seen[w.Hash] {
				continue
			}
			seen[w.Hash] = true
			stale2 = append(stale2, w)
		}

		tx, err := ix.Store.Begin(ctx)
		if err != nil {
			return st, err
		}
		for _, w := range stale2 {
			var res match.Result
			switch {
			case !w.HasSpec && w.ParseStatus == ParseArchived:
				st.NeverParsed++
				continue
			case !w.HasSpec:
				res = match.NoSpec(rf)
				st.NoSpec++
			case w.FunctionsJSON == "":
				st.NeedsRefetch++
				continue
			default:
				var fns []sepmeta.FnSig
				if err := json.Unmarshal([]byte(w.FunctionsJSON), &fns); err != nil {
					_ = tx.Rollback() // the decode error is the one to report
					return st, fmt.Errorf("functions_json of %s: %w", w.Hash, err)
				}
				res = ix.Matcher.Match(fns, rf)
				st.Recomputed++
			}
			if err := ix.writeMatches(ctx, tx, w.Hash, []match.Result{res}); err != nil {
				_ = tx.Rollback() // the write error is the one to report
				return st, err
			}
		}
		if err := tx.Commit(); err != nil {
			return st, err
		}
	}
	var versions []string
	for _, rf := range ix.Rules {
		versions = append(versions, rf.RulesetVersion)
	}
	return st, ix.Store.SetState(ctx, store.KeyRulesetVersions, strings.Join(versions, ","))
}

// findWasmWithHashChange finds Wasm that have an interface_matches row with a
// rule_file_sha256 that differs from the SHA-256 of the currently loaded rule
// file for the given SEP and ruleset version. Also includes rows where
// rule_file_sha256 is 'unknown' (written before migration 0003).
func (ix *Indexer) findWasmWithHashChange(ctx context.Context, sep int, rulesetVersion string) ([]store.StaleWasm, error) {
	// Compute the current rule file's SHA-256.
	var currentSHA256 string
	data, err := rules.FS.ReadFile(fmt.Sprintf("sep-%04d.json", sep))
	if err == nil {
		h := sha256.Sum256(data)
		currentSHA256 = hex.EncodeToString(h[:])
	}

	// Query for Wasm with matching ruleset_version but different/or unknown SHA-256.
	rows, err := ix.Store.DB.QueryContext(ctx, `
SELECT w.hash, w.has_spec, w.functions_json, w.parse_status
FROM wasm w
JOIN interface_matches im ON im.wasm_hash = w.hash AND im.sep = ?
WHERE im.ruleset_version = ?
  AND (im.rule_file_sha256 != ? OR im.rule_file_sha256 = 'unknown')
ORDER BY w.hash`, sep, rulesetVersion, currentSHA256)
	if err != nil {
		return nil, fmt.Errorf("stale by hash change: %w", err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	var out []store.StaleWasm
	for rows.Next() {
		var w store.StaleWasm
		var fns sql.NullString
		if err := rows.Scan(&w.Hash, &w.HasSpec, &fns, &w.ParseStatus); err != nil {
			return nil, fmt.Errorf("stale by hash change: %w", err)
		}
		w.FunctionsJSON = fns.String
		out = append(out, w)
	}
	return out, rows.Err()
}