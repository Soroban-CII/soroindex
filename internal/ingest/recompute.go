package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// RecomputeStats summarizes a --recompute run.
type RecomputeStats struct {
	Recomputed   int // matched again from functions_json
	NoSpec       int // parsed, no spec: recorded as no_spec
	NeedsRefetch int // has a spec but no functions_json (analyzed before migration 0002)
	NeverParsed  int // archived before it could ever be parsed: nothing to match
}

// Recompute re-runs the matcher for every Wasm that has no match row under
// the loaded ruleset versions, from stored functions_json, without fetching
// anything (CLAUDE.md §5.9 step 6). Rows for older ruleset versions are
// left in place as history. Each rule file is one transaction.
func (ix *Indexer) Recompute(ctx context.Context) (RecomputeStats, error) {
	var st RecomputeStats
	for _, rf := range ix.Rules {
		stale, err := ix.Store.StaleForRuleset(ctx, rf.SEP, rf.RulesetVersion)
		if err != nil {
			return st, err
		}
		tx, err := ix.Store.Begin(ctx)
		if err != nil {
			return st, err
		}
		for _, w := range stale {
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
