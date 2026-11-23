package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Checkpoint is a ledger and its hash, recorded with every committed sync
// batch. Ledger numbers restart after a testnet reset under the same
// passphrase; a hash cannot repeat across the two histories.
type Checkpoint struct {
	Ledger uint32
	Hash   string // 64 lowercase hex characters
}

// ErrBadCheckpoint means sync_state holds a checkpoint this code cannot read.
var ErrBadCheckpoint = errors.New("invalid ledger checkpoint")

func (c Checkpoint) String() string { return strconv.FormatUint(uint64(c.Ledger), 10) + ":" + c.Hash }

func parseCheckpoint(v string) (Checkpoint, error) {
	seq, hash, ok := strings.Cut(v, ":")
	n, err := strconv.ParseUint(seq, 10, 32)
	if !ok || err != nil || n == 0 || len(hash) != 64 || hash != strings.ToLower(hash) {
		return Checkpoint{}, fmt.Errorf("%w: %q", ErrBadCheckpoint, v)
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return Checkpoint{}, fmt.Errorf("%w: %q", ErrBadCheckpoint, v)
	}
	return Checkpoint{Ledger: uint32(n), Hash: hash}, nil
}

// SetCheckpoint records the last committed ledger's hash. Call it in the
// same transaction as SetLastLedger.
func (t *Tx) SetCheckpoint(ctx context.Context, c Checkpoint) error {
	if _, err := parseCheckpoint(c.String()); err != nil {
		return err
	}
	return t.SetState(ctx, KeyLedgerCheckpoint, c.String())
}

// Checkpoint reads the stored ledger checkpoint; ErrNotFound when an index
// has never committed a sync batch (a fresh or seed-only index).
func (s *Store) Checkpoint(ctx context.Context) (Checkpoint, error) {
	v, err := s.State(ctx, KeyLedgerCheckpoint)
	if err != nil {
		return Checkpoint{}, err
	}
	return parseCheckpoint(v)
}

// SetLastProtocol records the protocol of last_ledger. Call it in the same
// transaction as SetLastLedger.
func (t *Tx) SetLastProtocol(ctx context.Context, p uint32) error {
	return t.SetState(ctx, KeyLastProtocol, strconv.FormatUint(uint64(p), 10))
}

// LastProtocol reads the protocol of last_ledger; ok is false when no sync
// batch has recorded one yet.
func (s *Store) LastProtocol(ctx context.Context) (p uint32, ok bool, err error) {
	return readProtocol(ctx, s.DB)
}

// LastProtocol inside a batch transaction (see Tx.State for why).
func (t *Tx) LastProtocol(ctx context.Context) (p uint32, ok bool, err error) {
	return readProtocol(ctx, t.tx)
}

func readProtocol(ctx context.Context, q reader) (uint32, bool, error) {
	var v string
	err := q.QueryRowContext(ctx, `SELECT value FROM sync_state WHERE key = ?`, KeyLastProtocol).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("sync_state %s: %w", KeyLastProtocol, err)
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return 0, false, fmt.Errorf("invalid %s %q: %w", KeyLastProtocol, v, err)
	}
	return uint32(n), true, nil
}

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
