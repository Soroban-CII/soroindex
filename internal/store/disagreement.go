package store

import (
	"context"
)

// DisagreementStatus is the relationship between a contract's declared tier
// and its inferred interface match for the same SEP.
// The four values are: partial, mismatch, no_spec, absent-rule.
// absent-rule means the contract declares a SEP for which no rule file is loaded.
type DisagreementStatus string

const (
	// DisagreementPartial: declared + inferred status is "partial" (not all functions match).
	DisagreementPartial DisagreementStatus = "partial"
	// DisagreementMismatch: declared + inferred status is "mismatch" (fewer than half match).
	DisagreementMismatch DisagreementStatus = "mismatch"
	// DisagreementNoSpec: declared but no contractspecv0 section present.
	DisagreementNoSpec DisagreementStatus = "no_spec"
	// DisagreementAbsentRule: declared but no ruleset is loaded for this SEP.
	DisagreementAbsentRule DisagreementStatus = "absent-rule"
)

// DisagreementRow is one contract/SEP pair where the declared and inferred
// tiers diverge.
type DisagreementRow struct {
	ContractID     string             `json:"contract_id"`
	SEP            int                `json:"sep"`
	Status         DisagreementStatus `json:"status"`
	RulesetVersion string             `json:"ruleset_version,omitempty"`
}

// DisagreementFilter returns contracts that declare a SEP but whose inferred
// status shows a gap, filtered to the given statuses. Passing an empty statuses
// slice returns rows with any of the four disagreement statuses.
//
// This is a store-layer filter. It has no public API exposure: adding API
// params for this requires operator approval (CLAUDE.md §5.11). The proposed
// query params are documented in the PR for issue #40.
//
// All four status values correspond to rows in interface_matches or to the
// absence of a matching ruleset entry. The filter uses keyset pagination over
// (contract_id, sep); SACs are excluded because they are never in the declared tier.
func (s *Store) DisagreementFilter(ctx context.Context, statuses []DisagreementStatus, rulesets map[int]string, limit int, afterID string, afterSEP int) ([]DisagreementRow, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		return nil, ErrBadQuery
	}

	// Build the status allow-set; if empty, include all four values.
	want := statuses
	if len(want) == 0 {
		want = []DisagreementStatus{DisagreementPartial, DisagreementMismatch, DisagreementNoSpec, DisagreementAbsentRule}
	}
	statusSet := map[DisagreementStatus]bool{}
	for _, ds := range want {
		switch ds {
		case DisagreementPartial, DisagreementMismatch, DisagreementNoSpec, DisagreementAbsentRule:
			statusSet[ds] = true
		default:
			return nil, ErrBadQuery
		}
	}

	// The LEFT JOIN surfaces both mismatched rows and undeclared-match rows.
	// im.status IS NULL means no spec section was found; we coerce to 'no_spec'.
	// SACs are excluded unconditionally.
	rows, err := s.DB.QueryContext(ctx, `
		SELECT
			c.contract_id,
			wc.sep,
			COALESCE(im.status, 'no_spec') AS match_status,
			COALESCE(im.ruleset_version, '') AS ruleset_version
		FROM contracts c
		JOIN wasm_claims wc ON wc.wasm_hash = c.current_wasm_hash
		LEFT JOIN interface_matches im
			ON  im.wasm_hash = c.current_wasm_hash
			AND im.sep       = wc.sep
		WHERE c.archived = 0
		  AND c.kind <> 'sac'
		  AND (im.status IS NULL OR im.status IN ('partial','mismatch','no_spec'))
		  AND (c.contract_id > ? OR (c.contract_id = ? AND wc.sep > ?))
		ORDER BY c.contract_id, wc.sep
		LIMIT ?`,
		afterID, afterID, afterSEP, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []DisagreementRow
	for rows.Next() {
		var r DisagreementRow
		var matchStatus, rulesetVersion string
		if err := rows.Scan(&r.ContractID, &r.SEP, &matchStatus, &rulesetVersion); err != nil {
			return nil, err
		}

		// Distinguish no_spec from absent-rule using the loaded rulesets map.
		var ds DisagreementStatus
		if matchStatus == "no_spec" {
			if _, hasRule := rulesets[r.SEP]; !hasRule {
				ds = DisagreementAbsentRule
			} else {
				ds = DisagreementNoSpec
			}
		} else {
			ds = DisagreementStatus(matchStatus)
		}

		if !statusSet[ds] {
			continue
		}
		r.Status = ds
		r.RulesetVersion = rulesetVersion
		out = append(out, r)
	}
	return out, rows.Err()
}
