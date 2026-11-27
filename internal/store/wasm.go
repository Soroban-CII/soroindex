package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// ValidateWasmHash accepts only canonical, lowercase SHA-256 hashes.
func ValidateWasmHash(hash string) error {
	if len(hash) != 64 || hash != strings.ToLower(hash) {
		return ErrBadQuery
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return ErrBadQuery
	}
	return nil
}

// WasmClaim preserves the original declaration token and any anomaly.
type WasmClaim struct {
	SEP      int     `json:"sep"`
	Source   string  `json:"source"`
	RawToken string  `json:"raw_token"`
	Anomaly  *string `json:"anomaly"`
}

// WasmAnomaly is a token that produced no claim.
type WasmAnomaly struct {
	RawToken string `json:"raw_token"`
	Anomaly  string `json:"anomaly"`
}

// WasmMatch includes the ruleset and full interface evidence.
type WasmMatch struct {
	SEP             int             `json:"sep"`
	RulesetVersion  string          `json:"ruleset_version"`
	Status          string          `json:"status"`
	OKCount         int             `json:"ok_count"`
	Missing         json.RawMessage `json:"missing"`
	Mismatched      json.RawMessage `json:"mismatched"`
	MatchedVariants json.RawMessage `json:"matched_variants"`
	// MatchedVersion: see InferredDetail.MatchedVersion.
	MatchedVersion *string `json:"matched_version"`
}

// WasmDetail exposes stored parsing evidence and current users of the code.
type WasmDetail struct {
	Hash          string            `json:"wasm_hash"`
	Meta          json.RawMessage   `json:"meta"`
	Claims        []WasmClaim       `json:"claims"`
	Anomalies     []WasmAnomaly     `json:"anomalies"`
	Matches       []WasmMatch       `json:"interface_matches"`
	Contracts     []ContractSummary `json:"contracts"`
	NextCursor    *string           `json:"next_cursor"`
	ParseStatus   string            `json:"parse_status"`
	ParseError    *string           `json:"parse_error"`
	ParserVersion string            `json:"parser_version"`
	AsOfLedger    uint32            `json:"as_of_ledger"`
}

// WasmFunctions is a Wasm's stored interface, with the two facts a caller
// needs to tell "this code declares no functions" apart from "the index does
// not know its functions". HasSpec says the code carries a contractspecv0
// section; Stored says functions_json holds the signatures read from it. A
// Wasm analyzed before migration 0002 has a spec and no signatures, and must
// be re-fetched rather than reported as having an empty interface.
type WasmFunctions struct {
	Functions []sepmeta.FnSig `json:"functions"`
	HasSpec   bool            `json:"has_spec"`
	Stored    bool            `json:"stored"`
}

// FunctionEvidence reads has_spec and functions_json together, so a caller
// never has to guess why signatures are absent. An unknown hash is
// ErrNotFound; Store.Functions cannot report that, because it treats a
// missing row and a missing column alike.
func (s *Store) FunctionEvidence(ctx context.Context, hash string) (WasmFunctions, error) {
	var out WasmFunctions
	if err := ValidateWasmHash(hash); err != nil {
		return out, err
	}
	var raw sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT has_spec,functions_json FROM wasm WHERE hash = ?`, hash).Scan(&out.HasSpec, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, fmt.Errorf("function evidence of %s: %w", hash, err)
	}
	if !raw.Valid {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw.String), &out.Functions); err != nil {
		return out, fmt.Errorf("functions_json of %s: %w", hash, err)
	}
	out.Stored = true
	return out, nil
}

// Wasm reads parsing data and a bounded page of live users in one snapshot.
func (s *Store) Wasm(ctx context.Context, hash string, limit int, cursor string, rulesets map[int]string) (WasmDetail, error) {
	d := WasmDetail{Hash: hash, Meta: json.RawMessage("null"), Claims: []WasmClaim{}, Anomalies: []WasmAnomaly{}, Matches: []WasmMatch{}, Contracts: []ContractSummary{}}
	if err := ValidateWasmHash(hash); err != nil {
		return d, err
	}
	limit, after, err := contractPageInput(limit, cursor)
	if err != nil {
		return d, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return d, err
	}
	defer func() { _ = tx.Rollback() }()
	var meta sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT meta_json,parse_status,parse_error,parser_version FROM wasm WHERE hash = ?`, hash).Scan(&meta, &d.ParseStatus, &d.ParseError, &d.ParserVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if meta.Valid {
		if !json.Valid([]byte(meta.String)) {
			return d, errors.New("invalid stored metadata JSON")
		}
		d.Meta = json.RawMessage(meta.String)
	}
	d.AsOfLedger, err = readLedger(ctx, tx)
	if err != nil {
		return d, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT sep,source,raw_token,anomaly FROM wasm_claims WHERE wasm_hash = ? ORDER BY sep,source`, hash)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var c WasmClaim
		if err = rows.Scan(&c.SEP, &c.Source, &c.RawToken, &c.Anomaly); err != nil {
			break
		}
		d.Claims = append(d.Claims, c)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return d, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT raw_token,anomaly FROM parse_anomalies WHERE wasm_hash = ? ORDER BY raw_token`, hash)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var a WasmAnomaly
		if err = rows.Scan(&a.RawToken, &a.Anomaly); err != nil {
			break
		}
		d.Anomalies = append(d.Anomalies, a)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return d, err
	}
	versions, err := json.Marshal(rulesets)
	if err != nil {
		return d, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT sep,ruleset_version,status,ok_count,missing_json,mismatched_json,matched_variants_json FROM interface_matches WHERE wasm_hash = ? AND ruleset_version = json_extract(?, '$.' || sep) ORDER BY sep`, hash, string(versions))
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var m WasmMatch
		var missing, mismatched, variants string
		if err = rows.Scan(&m.SEP, &m.RulesetVersion, &m.Status, &m.OKCount, &missing, &mismatched, &variants); err != nil {
			break
		}
		if !json.Valid([]byte(missing)) || !json.Valid([]byte(mismatched)) || !json.Valid([]byte(variants)) {
			err = errors.New("invalid stored match JSON")
			break
		}
		m.Missing = json.RawMessage(missing)
		m.Mismatched = json.RawMessage(mismatched)
		m.MatchedVariants = json.RawMessage(variants)
		d.Matches = append(d.Matches, m)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return d, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT contract_id,kind,current_wasm_hash,exec_ref_owner,exec_ref_tag,archived FROM contracts WHERE current_wasm_hash = ? AND archived = 0 AND contract_id > ? ORDER BY contract_id LIMIT ?`, hash, after, limit+1)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var c ContractSummary
		var owner, tag sql.NullString
		if err = rows.Scan(&c.ID, &c.Kind, &c.WasmHash, &owner, &tag, &c.Archived); err != nil {
			break
		}
		if c.Kind == "wasm_ref" {
			c.ExecRef = &Reference{owner.String, tag.String}
		}
		d.Contracts = append(d.Contracts, c)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return d, err
	}
	if len(d.Contracts) > limit {
		d.Contracts = d.Contracts[:limit]
		c := encodeContractCursor(d.Contracts[limit-1].ID)
		d.NextCursor = &c
	}
	return d, tx.Commit()
}
