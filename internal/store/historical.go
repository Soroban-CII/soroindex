package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// ContractAtLedger returns the contract detail as it existed at a specific
// ledger sequence number. It selects the version whose [from_ledger, to_ledger)
// interval contains the requested ledger, using a half-open range:
//
//	from_ledger <= ledger AND (to_ledger IS NULL OR to_ledger > ledger)
//
// This satisfies the half-open version range semantics specified in issue #41:
// an upgrade at ledger L closes [from, L) and opens [L, nil), so a query at
// exactly L returns the new version, and a query at L-1 returns the old one.
//
// The returned ContractDetail.AsOfLedger is set to the requested ledger, not
// the current sync head. Claims are built from the historical Wasm hash, with
// verifications marked stale when that hash differs from the current one.
//
// This is a store-layer function. It has no public API exposure: adding an
// as_of_ledger query param requires operator approval (CLAUDE.md §5.11). The
// proposed param is documented in the PR for issue #41.
func (s *Store) ContractAtLedger(ctx context.Context, id string, ledger uint32, network string, rulesets map[int]string) (ContractDetail, error) {
	if err := ValidateContractID(id); err != nil {
		return ContractDetail{}, err
	}

	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ContractDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()

	// Read the base contract row.
	var d ContractDetail
	d.ContractID = id
	d.Network = network
	d.AsOfLedger = ledger
	d.ParserVersion = sepmeta.ParserVersion

	var owner, tag sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT kind, current_wasm_hash, exec_ref_owner, exec_ref_tag, archived
		 FROM contracts WHERE contract_id = ?`, id).
		Scan(&d.Kind, &d.WasmHash, &owner, &tag, &d.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		return ContractDetail{}, ErrNotFound
	}
	if err != nil {
		return ContractDetail{}, err
	}
	if d.Kind == "wasm_ref" {
		d.ExecRef = &Reference{owner.String, tag.String}
	}

	// Find the version active at the requested ledger.
	var historicalHash sql.NullString
	var historicalOwner, historicalTag sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT wasm_hash, exec_ref_owner, exec_ref_tag
		FROM contract_versions
		WHERE contract_id = ?
		  AND from_ledger <= ?
		  AND (to_ledger IS NULL OR to_ledger > ?)
		ORDER BY from_ledger DESC
		LIMIT 1`,
		id, ledger, ledger).
		Scan(&historicalHash, &historicalOwner, &historicalTag)
	if errors.Is(err, sql.ErrNoRows) {
		// Contract did not exist at this ledger.
		return ContractDetail{}, ErrNotFound
	}
	if err != nil {
		return ContractDetail{}, err
	}

	// Build the response using the historical hash as the point-in-time view.
	var historicalHashPtr *string
	if historicalHash.Valid {
		h := historicalHash.String
		historicalHashPtr = &h
	}
	d.WasmHash = historicalHashPtr
	if historicalOwner.Valid {
		d.ExecRef = &Reference{historicalOwner.String, historicalTag.String}
	}

	// Claims are built from the historical hash; verifications are marked stale
	// when the historical hash differs from the current one.
	protocol := d.Kind == "sac"
	d.Claims, err = claimsFor(ctx, tx, id, historicalHashPtr, d.WasmHash, protocol, rulesets)
	if err != nil {
		return d, err
	}

	// History: return the full version range so callers can see the boundary.
	d.History, err = summaries(ctx, tx, id)
	if err != nil {
		return d, err
	}

	return d, tx.Commit()
}

