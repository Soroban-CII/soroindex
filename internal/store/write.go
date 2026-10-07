package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Tx is one write transaction. The indexer writes each batch (a seed chunk
// or a range of ledgers) in a single Tx, so a crash leaves either all of it
// or none of it. Every write is idempotent: replaying a batch changes
// nothing.
type Tx struct {
	tx *sql.Tx
}

// Begin starts a write transaction.
func (s *Store) Begin(ctx context.Context) (*Tx, error) {
	if s.readOnly {
		return nil, errors.New("store: read-only")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	return &Tx{tx: tx}, nil
}

// Commit commits the transaction.
func (t *Tx) Commit() error { return t.tx.Commit() }

// Rollback abandons the transaction; it is a no-op after Commit.
func (t *Tx) Rollback() error {
	err := t.tx.Rollback()
	if errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return err
}

func (t *Tx) exec(ctx context.Context, what, q string, args ...any) error {
	if _, err := t.tx.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// SetState writes a sync_state value inside the transaction.
func (t *Tx) SetState(ctx context.Context, key, value string) error {
	return t.exec(ctx, "set "+key,
		`INSERT INTO sync_state (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
}

// State reads a sync_state value inside the transaction. Use this, not
// Store.State, while a write transaction is open: the writer has a single
// connection, so a query outside the transaction would wait for it forever.
func (t *Tx) State(ctx context.Context, key string) (string, error) {
	var v string
	err := t.tx.QueryRowContext(ctx, `SELECT value FROM sync_state WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("sync_state %s: %w", key, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("sync_state %s: %w", key, err)
	}
	return v, nil
}

// SetLastLedger records the last ledger fully applied.
func (t *Tx) SetLastLedger(ctx context.Context, l uint32) error {
	return t.SetState(ctx, KeyLastLedger, strconv.FormatUint(uint64(l), 10))
}

// WasmRow is one row of the wasm table.
type WasmRow struct {
	Hash            string
	SizeBytes       int
	FirstSeenLedger uint32
	HasMeta         bool
	HasSpec         bool
	SEPEntryCount   int
	MetaJSON        string // JSON array of {"key","value"}; "" for NULL
	FunctionsJSON   string // JSON array of sepmeta.FnSig; "" for NULL
	ParseStatus     string
	ParseError      string
	ParserVersion   string
}

// UpsertWasm inserts or updates a Wasm row. first_seen_ledger keeps the
// earliest value ever written. An "archived" row never overwrites a parsed
// one: the indexer keeps the last known data when code is archived
// (CLAUDE.md §5.9).
func (t *Tx) UpsertWasm(ctx context.Context, w WasmRow) error {
	return t.exec(ctx, "upsert wasm "+w.Hash, `
INSERT INTO wasm (hash, size_bytes, first_seen_ledger, has_meta, has_spec, sep_entry_count, meta_json, functions_json, parse_status, parse_error, parser_version)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (hash) DO UPDATE SET
  first_seen_ledger = CASE
    WHEN wasm.first_seen_ledger IS NULL THEN excluded.first_seen_ledger
    WHEN excluded.first_seen_ledger IS NULL THEN wasm.first_seen_ledger
    ELSE min(wasm.first_seen_ledger, excluded.first_seen_ledger) END,
  size_bytes      = CASE WHEN excluded.parse_status = 'archived' THEN wasm.size_bytes      ELSE excluded.size_bytes END,
  has_meta        = CASE WHEN excluded.parse_status = 'archived' THEN wasm.has_meta        ELSE excluded.has_meta END,
  has_spec        = CASE WHEN excluded.parse_status = 'archived' THEN wasm.has_spec        ELSE excluded.has_spec END,
  sep_entry_count = CASE WHEN excluded.parse_status = 'archived' THEN wasm.sep_entry_count ELSE excluded.sep_entry_count END,
  meta_json       = CASE WHEN excluded.parse_status = 'archived' THEN wasm.meta_json       ELSE excluded.meta_json END,
  functions_json  = CASE WHEN excluded.parse_status = 'archived' THEN wasm.functions_json  ELSE excluded.functions_json END,
  parse_error     = CASE WHEN excluded.parse_status = 'archived' AND wasm.parse_status <> 'archived' THEN wasm.parse_error ELSE excluded.parse_error END,
  parser_version  = CASE WHEN excluded.parse_status = 'archived' AND wasm.parse_status <> 'archived' THEN wasm.parser_version ELSE excluded.parser_version END,
  parse_status    = CASE WHEN excluded.parse_status = 'archived' AND wasm.parse_status <> 'archived' THEN wasm.parse_status ELSE excluded.parse_status END`,
		w.Hash, nullInt(w.SizeBytes, w.ParseStatus != "archived"), nullLedger(w.FirstSeenLedger), w.HasMeta, w.HasSpec, w.SEPEntryCount,
		nullStr(w.MetaJSON), nullStr(w.FunctionsJSON), w.ParseStatus, nullStr(w.ParseError), w.ParserVersion)
}

// ClaimRow is one wasm_claims row.
type ClaimRow struct {
	SEP      int
	RawToken string
	Anomaly  string
}

// ReplaceClaims sets the claims one source makes for a Wasm. Claims the
// source no longer makes (after a parser change) are removed; the rest are
// upserted. Callers never call it for archived code, so archival never
// deletes claims.
func (t *Tx) ReplaceClaims(ctx context.Context, hash, source string, claims []ClaimRow) error {
	keep, err := json.Marshal(seps(claims))
	if err != nil {
		return err
	}
	if err := t.exec(ctx, "prune claims "+hash,
		`DELETE FROM wasm_claims WHERE wasm_hash = ? AND source = ? AND sep NOT IN (SELECT value FROM json_each(?))`,
		hash, source, string(keep)); err != nil {
		return err
	}
	for _, c := range claims {
		if err := t.exec(ctx, "upsert claim "+hash, `
INSERT INTO wasm_claims (wasm_hash, sep, source, raw_token, anomaly) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (wasm_hash, sep, source) DO UPDATE SET raw_token = excluded.raw_token, anomaly = excluded.anomaly`,
			hash, c.SEP, source, c.RawToken, nullStr(c.Anomaly)); err != nil {
			return err
		}
	}
	return nil
}

func seps(cs []ClaimRow) []int {
	out := make([]int, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.SEP)
	}
	return out
}

// AnomalyRow is a token that produced no SEP.
type AnomalyRow struct {
	RawToken string
	Anomaly  string
}

// ReplaceAnomalies sets a Wasm's parse anomalies (tokens producing no SEP).
func (t *Tx) ReplaceAnomalies(ctx context.Context, hash string, rows []AnomalyRow) error {
	if err := t.exec(ctx, "clear anomalies "+hash, `DELETE FROM parse_anomalies WHERE wasm_hash = ?`, hash); err != nil {
		return err
	}
	for _, a := range rows {
		if err := t.exec(ctx, "insert anomaly "+hash,
			`INSERT INTO parse_anomalies (wasm_hash, raw_token, anomaly) VALUES (?, ?, ?) ON CONFLICT (wasm_hash, raw_token) DO NOTHING`,
			hash, a.RawToken, a.Anomaly); err != nil {
			return err
		}
	}
	return nil
}

// MatchRow is one interface_matches row; the JSON fields are pre-encoded.
type MatchRow struct {
	Hash                string
	SEP                 int
	RulesetVersion      string
	Status              string
	OKCount             int
	MissingJSON         string
	MismatchedJSON      string
	MatchedVariantsJSON string
	ComputedAt          string // RFC 3339 UTC
}

// UpsertMatch writes one match result.
func (t *Tx) UpsertMatch(ctx context.Context, m MatchRow) error {
	return t.exec(ctx, "upsert match "+m.Hash, `
INSERT INTO interface_matches (wasm_hash, sep, ruleset_version, status, ok_count, missing_json, mismatched_json, matched_variants_json, computed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (wasm_hash, sep, ruleset_version) DO UPDATE SET
  status = excluded.status, ok_count = excluded.ok_count, missing_json = excluded.missing_json,
  mismatched_json = excluded.mismatched_json, matched_variants_json = excluded.matched_variants_json,
  computed_at = CASE WHEN interface_matches.status = excluded.status AND interface_matches.ok_count = excluded.ok_count
                      AND interface_matches.missing_json = excluded.missing_json AND interface_matches.mismatched_json = excluded.mismatched_json
                     THEN interface_matches.computed_at ELSE excluded.computed_at END`,
		m.Hash, m.SEP, m.RulesetVersion, m.Status, m.OKCount, m.MissingJSON, m.MismatchedJSON, m.MatchedVariantsJSON, m.ComputedAt)
}

// ContractRow is the current state of a contract.
type ContractRow struct {
	ID              string
	Kind            string // wasm | wasm_ref | sac
	CurrentWasmHash string // "" for NULL
	ExecRefOwner    string
	ExecRefTag      string
	SACAsset        string
	CreatedLedger   uint32 // 0 for unknown
	UpdatedLedger   uint32
	Archived        bool
}

// UpsertContract writes a contract's current state. created_ledger keeps
// the first known value; updated_ledger never moves backwards, so replaying
// an older batch cannot regress it.
func (t *Tx) UpsertContract(ctx context.Context, c ContractRow) error {
	return t.exec(ctx, "upsert contract "+c.ID, `
INSERT INTO contracts (contract_id, kind, current_wasm_hash, exec_ref_owner, exec_ref_tag, sac_asset, created_ledger, updated_ledger, archived)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (contract_id) DO UPDATE SET
  kind = excluded.kind, current_wasm_hash = excluded.current_wasm_hash,
  exec_ref_owner = excluded.exec_ref_owner, exec_ref_tag = excluded.exec_ref_tag,
  sac_asset = COALESCE(excluded.sac_asset, contracts.sac_asset),
  created_ledger = COALESCE(contracts.created_ledger, excluded.created_ledger),
  updated_ledger = CASE WHEN excluded.updated_ledger IS NULL THEN contracts.updated_ledger
                        WHEN contracts.updated_ledger IS NULL THEN excluded.updated_ledger
                        ELSE max(contracts.updated_ledger, excluded.updated_ledger) END,
  archived = excluded.archived`,
		c.ID, c.Kind, nullStr(c.CurrentWasmHash), nullStr(c.ExecRefOwner), nullStr(c.ExecRefTag), nullStr(c.SACAsset),
		nullLedger(c.CreatedLedger), nullLedger(c.UpdatedLedger), c.Archived)
}

// SetContractArchived marks a contract archived or live without touching
// anything else: archival never deletes claims or history.
func (t *Tx) SetContractArchived(ctx context.Context, id string, archived bool) error {
	return t.exec(ctx, "archive contract "+id, `UPDATE contracts SET archived = ? WHERE contract_id = ?`, archived, id)
}

// Version is one contract_versions row.
type Version struct {
	ContractID   string
	WasmHash     string // "" for NULL
	ExecRefOwner string
	ExecRefTag   string
	FromLedger   uint32
	ToLedger     uint32 // 0 while current
}

// CurrentVersion returns a contract's open version row.
func (t *Tx) CurrentVersion(ctx context.Context, id string) (Version, error) {
	var v Version
	var hash, owner, tag sql.NullString
	var from int64
	err := t.tx.QueryRowContext(ctx, `SELECT wasm_hash, exec_ref_owner, exec_ref_tag, from_ledger FROM contract_versions
		WHERE contract_id = ? AND to_ledger IS NULL`, id).Scan(&hash, &owner, &tag, &from)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, fmt.Errorf("current version of %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Version{}, fmt.Errorf("current version of %s: %w", id, err)
	}
	l, err := toLedger(from)
	if err != nil {
		return Version{}, err
	}
	v = Version{ContractID: id, WasmHash: hash.String, ExecRefOwner: owner.String, ExecRefTag: tag.String, FromLedger: l}
	return v, nil
}

// SetVersion makes v the contract's current version as of v.FromLedger.
// If the open row already has the same code and reference, nothing changes
// (idempotent replay). Otherwise the open row is closed at v.FromLedger and
// a new one opened — that is an upgrade. A row already starting at
// v.FromLedger is updated in place, so re-applying a ledger never creates a
// second row.
func (t *Tx) SetVersion(ctx context.Context, v Version) (changed bool, err error) {
	cur, err := t.CurrentVersion(ctx, v.ContractID)
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return false, err
	case cur.WasmHash == v.WasmHash && cur.ExecRefOwner == v.ExecRefOwner && cur.ExecRefTag == v.ExecRefTag:
		return false, nil
	case cur.FromLedger == v.FromLedger:
		return true, t.exec(ctx, "rewrite version "+v.ContractID, `UPDATE contract_versions
			SET wasm_hash = ?, exec_ref_owner = ?, exec_ref_tag = ? WHERE contract_id = ? AND from_ledger = ?`,
			nullStr(v.WasmHash), nullStr(v.ExecRefOwner), nullStr(v.ExecRefTag), v.ContractID, v.FromLedger)
	case v.FromLedger < cur.FromLedger:
		// An older ledger replayed after a newer one: history is already
		// ahead, so leave it.
		return false, nil
	default:
		if err := t.exec(ctx, "close version "+v.ContractID,
			`UPDATE contract_versions SET to_ledger = ? WHERE contract_id = ? AND to_ledger IS NULL`, v.FromLedger, v.ContractID); err != nil {
			return false, err
		}
	}
	return true, t.exec(ctx, "open version "+v.ContractID, `INSERT INTO contract_versions
		(contract_id, wasm_hash, exec_ref_owner, exec_ref_tag, from_ledger, to_ledger) VALUES (?, ?, ?, ?, ?, NULL)
		ON CONFLICT (contract_id, from_ledger) DO UPDATE SET wasm_hash = excluded.wasm_hash,
		  exec_ref_owner = excluded.exec_ref_owner, exec_ref_tag = excluded.exec_ref_tag, to_ledger = NULL`,
		v.ContractID, nullStr(v.WasmHash), nullStr(v.ExecRefOwner), nullStr(v.ExecRefTag), v.FromLedger)
}

// UpsertExecRef records a CAP-85 reference entry's current hash.
func (t *Tx) UpsertExecRef(ctx context.Context, owner, tag, hash string, ledger uint32, archived bool) error {
	return t.exec(ctx, "upsert exec ref", `
INSERT INTO exec_refs (owner_contract_id, tag, wasm_hash, updated_ledger, archived) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (owner_contract_id, tag) DO UPDATE SET
  wasm_hash = COALESCE(excluded.wasm_hash, exec_refs.wasm_hash),
  updated_ledger = CASE WHEN excluded.updated_ledger IS NULL THEN exec_refs.updated_ledger
                        WHEN exec_refs.updated_ledger IS NULL THEN excluded.updated_ledger
                        ELSE max(exec_refs.updated_ledger, excluded.updated_ledger) END,
  archived = excluded.archived`,
		owner, tag, nullStr(hash), nullLedger(ledger), archived)
}

// HasWasm reports whether a Wasm row exists with a parsed (non-archived)
// status, so the indexer fetches each hash once.
func (t *Tx) HasWasm(ctx context.Context, hash string) (bool, error) {
	var n int
	err := t.tx.QueryRowContext(ctx, `SELECT count(*) FROM wasm WHERE hash = ? AND parse_status <> 'archived'`, hash).Scan(&n)
	return n > 0, err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullLedger(l uint32) any {
	if l == 0 {
		return nil
	}
	return int64(l)
}

func nullInt(n int, valid bool) any {
	if !valid {
		return nil
	}
	return n
}

// toLedger converts a stored INTEGER to a ledger number at the store
// boundary (CLAUDE.md §8), rejecting values that cannot be ledgers.
func toLedger(v int64) (uint32, error) {
	if v < 0 || v > int64(^uint32(0)) {
		return 0, fmt.Errorf("stored ledger %d out of range", v)
	}
	return uint32(v), nil
}
