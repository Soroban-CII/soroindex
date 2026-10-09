package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Contract returns current state inside a batch transaction.
func (t *Tx) Contract(ctx context.Context, id string) (ContractRow, error) {
	var c ContractRow
	var hash, owner, tag, asset sql.NullString
	var created, updated sql.NullInt64
	err := t.tx.QueryRowContext(ctx, `SELECT contract_id, kind, current_wasm_hash, exec_ref_owner, exec_ref_tag, sac_asset, created_ledger, updated_ledger, archived FROM contracts WHERE contract_id = ?`, id).Scan(&c.ID, &c.Kind, &hash, &owner, &tag, &asset, &created, &updated, &c.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, fmt.Errorf("contract %s: %w", id, err)
	}
	c.CurrentWasmHash, c.ExecRefOwner, c.ExecRefTag, c.SACAsset = hash.String, owner.String, tag.String, asset.String
	if c.CreatedLedger, err = toLedger(created.Int64); err != nil {
		return c, err
	}
	c.UpdatedLedger, err = toLedger(updated.Int64)
	return c, err
}

// ExecRef resolves a live reference; archived entries deliberately resolve to nothing.
func (t *Tx) ExecRef(ctx context.Context, owner, tag string) (hash string, archived bool, err error) {
	var h sql.NullString
	err = t.tx.QueryRowContext(ctx, `SELECT wasm_hash, archived FROM exec_refs WHERE owner_contract_id = ? AND tag = ?`, owner, tag).Scan(&h, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrNotFound
	}
	if err != nil {
		return "", false, err
	}
	if !archived {
		hash = h.String
	}
	return
}

// Referencing returns contracts affected by one executable-reference update.
func (t *Tx) Referencing(ctx context.Context, owner, tag string) ([]string, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT contract_id FROM contracts WHERE kind = 'wasm_ref' AND exec_ref_owner = ? AND exec_ref_tag = ? ORDER BY contract_id`, owner, tag)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // read-only rows
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SetCodeArchived updates availability without deleting parsed data or claims.
func (t *Tx) SetCodeArchived(ctx context.Context, hash string, archived bool) error {
	return t.exec(ctx, "archive code contracts", `UPDATE contracts SET archived = ? WHERE current_wasm_hash = ?`, archived, hash)
}

// WasmArchived reports code that has never been available for parsing.
func (t *Tx) WasmArchived(ctx context.Context, hash string) (bool, error) {
	var status string
	err := t.tx.QueryRowContext(ctx, `SELECT parse_status FROM wasm WHERE hash = ?`, hash).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return status == "archived", err
}
