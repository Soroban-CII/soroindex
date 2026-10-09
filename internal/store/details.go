package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// InferredDetail exposes interface evidence without treating it as a declaration.
type InferredDetail struct {
	Status         string          `json:"status"`
	RulesetVersion string          `json:"ruleset_version"`
	Missing        json.RawMessage `json:"missing"`
	Mismatched     json.RawMessage `json:"mismatched"`
}

// VerificationDetail records a suite result and whether it belongs to old code.
type VerificationDetail struct {
	Verdict     string `json:"verdict"`
	Tool        string `json:"tool"`
	ToolVersion string `json:"tool_version"`
	RunAt       string `json:"run_at"`
	Stale       bool   `json:"stale"`
}

// ClaimDetail keeps the four trust tiers independent for one SEP.
type ClaimDetail struct {
	SEP      int                 `json:"sep"`
	Declared bool                `json:"declared"`
	Inferred *InferredDetail     `json:"inferred"`
	Verified *VerificationDetail `json:"verified"`
	Protocol bool                `json:"protocol"`
	Source   string              `json:"source"`
	Anomaly  *string             `json:"anomaly"`
}

// VersionSummary exposes half-open ledger ranges and executable references.
type VersionSummary struct {
	WasmHash   *string    `json:"wasm_hash"`
	FromLedger uint32     `json:"from_ledger"`
	ToLedger   *uint32    `json:"to_ledger"`
	ExecRef    *Reference `json:"exec_ref"`
}

// ContractDetail implements the fixed contract response in CLAUDE.md section 5.11.
type ContractDetail struct {
	ContractID    string           `json:"contract_id"`
	Network       string           `json:"network"`
	Kind          string           `json:"kind"`
	WasmHash      *string          `json:"wasm_hash"`
	ExecRef       *Reference       `json:"exec_ref"`
	Archived      bool             `json:"archived"`
	Claims        []ClaimDetail    `json:"claims"`
	History       []VersionSummary `json:"history"`
	ParserVersion string           `json:"parser_version"`
	AsOfLedger    uint32           `json:"as_of_ledger"`
}

// HistoryVersion includes the claims at each historical executable.
type HistoryVersion struct {
	VersionSummary
	Claims []ClaimDetail `json:"claims"`
}

// HistoryDetail is the complete contract history with provenance.
type HistoryDetail struct {
	ContractID    string           `json:"contract_id"`
	Network       string           `json:"network"`
	History       []HistoryVersion `json:"history"`
	ParserVersion string           `json:"parser_version"`
	AsOfLedger    uint32           `json:"as_of_ledger"`
	NextCursor    *string          `json:"next_cursor"`
}

type reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readLedger(ctx context.Context, q reader) (uint32, error) {
	var value string
	err := q.QueryRowContext(ctx, `SELECT value FROM sync_state WHERE key = ?`, KeyLastLedger).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(value, 10, 32)
	return uint32(n), err // #nosec G115 -- parsed at 32 bits
}
func summaries(ctx context.Context, q reader, id string) ([]VersionSummary, error) {
	return versionRange(ctx, q, id, 0, 2147483647)
}

func versionRange(ctx context.Context, q reader, id string, after uint32, limit int) ([]VersionSummary, error) {
	rows, err := q.QueryContext(ctx, `SELECT wasm_hash,from_ledger,to_ledger,exec_ref_owner,exec_ref_tag FROM contract_versions WHERE contract_id = ? AND from_ledger > ? ORDER BY from_ledger LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []VersionSummary{}
	for rows.Next() {
		var v VersionSummary
		var from int64
		var to sql.NullInt64
		var owner, tag sql.NullString
		if err := rows.Scan(&v.WasmHash, &from, &to, &owner, &tag); err != nil {
			return nil, err
		}
		v.FromLedger, err = toLedger(from)
		if err != nil {
			return nil, err
		}
		if to.Valid {
			n, err := toLedger(to.Int64)
			if err != nil {
				return nil, err
			}
			v.ToLedger = &n
		}
		if owner.Valid {
			v.ExecRef = &Reference{owner.String, tag.String}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func claimsFor(ctx context.Context, q reader, id string, hash, currentHash *string, protocol bool, rulesets map[int]string) ([]ClaimDetail, error) {
	if protocol {
		return []ClaimDetail{{SEP: 41, Protocol: true, Source: "protocol"}}, nil
	}
	if hash == nil {
		return []ClaimDetail{}, nil
	}
	claims := map[int]*ClaimDetail{}
	get := func(sep int) *ClaimDetail {
		if claims[sep] == nil {
			claims[sep] = &ClaimDetail{SEP: sep, Source: "interface-match"}
		}
		return claims[sep]
	}
	rows, err := q.QueryContext(ctx, `SELECT sep,source,anomaly FROM wasm_claims WHERE wasm_hash = ? ORDER BY sep,source`, *hash)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sep int
		var source string
		var anomaly *string
		if err := rows.Scan(&sep, &source, &anomaly); err != nil {
			_ = rows.Close()
			return nil, err
		}
		c := get(sep)
		c.Declared = true
		c.Source = source
		c.Anomaly = anomaly
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	rules, err := json.Marshal(rulesets)
	if err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT sep,status,ruleset_version,missing_json,mismatched_json FROM interface_matches WHERE wasm_hash = ? AND ruleset_version = json_extract(?, '$.' || sep) ORDER BY sep`, *hash, string(rules))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sep int
		var d InferredDetail
		var missing, mismatched string
		if err := rows.Scan(&sep, &d.Status, &d.RulesetVersion, &missing, &mismatched); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !json.Valid([]byte(missing)) || !json.Valid([]byte(mismatched)) {
			_ = rows.Close()
			return nil, errors.New("invalid stored match JSON")
		}
		d.Missing = json.RawMessage(missing)
		d.Mismatched = json.RawMessage(mismatched)
		get(sep).Inferred = &d
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT sep,verdict,tool,tool_version,run_at FROM verifications WHERE contract_id = ? AND wasm_hash = ? ORDER BY run_at DESC, tool`, id, *hash)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sep int
		var v VerificationDetail
		if err := rows.Scan(&sep, &v.Verdict, &v.Tool, &v.ToolVersion, &v.RunAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		c := get(sep)
		if c.Verified == nil {
			v.Stale = currentHash == nil || *currentHash != *hash
			c.Verified = &v
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	seps := []int{}
	for sep := range claims {
		seps = append(seps, sep)
	}
	sort.Ints(seps)
	out := []ClaimDetail{}
	for _, sep := range seps {
		out = append(out, *claims[sep])
	}
	return out, nil
}
func contractDetail(ctx context.Context, q reader, id, network string, rulesets map[int]string) (ContractDetail, error) {
	d := ContractDetail{ContractID: id, Network: network, ParserVersion: sepmeta.ParserVersion, Claims: []ClaimDetail{}, History: []VersionSummary{}}
	var owner, tag sql.NullString
	err := q.QueryRowContext(ctx, `SELECT kind,current_wasm_hash,exec_ref_owner,exec_ref_tag,archived FROM contracts WHERE contract_id = ?`, id).Scan(&d.Kind, &d.WasmHash, &owner, &tag, &d.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if d.Kind == "wasm_ref" {
		d.ExecRef = &Reference{owner.String, tag.String}
	}
	d.AsOfLedger, err = readLedger(ctx, q)
	if err != nil {
		return d, err
	}
	d.History, err = summaries(ctx, q, id)
	if err != nil {
		return d, err
	}
	d.Claims, err = claimsFor(ctx, q, id, d.WasmHash, d.WasmHash, d.Kind == "sac", rulesets)
	return d, err
}

// Contract returns a consistent snapshot of current claims and version ranges.
func (s *Store) Contract(ctx context.Context, id, network string, rulesets map[int]string) (ContractDetail, error) {
	if err := ValidateContractID(id); err != nil {
		return ContractDetail{}, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ContractDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()
	d, err := contractDetail(ctx, tx, id, network, rulesets)
	if err != nil {
		return d, err
	}
	return d, tx.Commit()
}

// History returns claims for every version, with old verifications marked stale.
func (s *Store) History(ctx context.Context, id, network string, rulesets map[int]string) (HistoryDetail, error) {
	return s.history(ctx, id, network, rulesets, 2147483646, "")
}

type historyCursor struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
	Ledger  uint32 `json:"ledger"`
}

// HistoryPage bounds work by selecting version ranges before loading claims.
func (s *Store) HistoryPage(ctx context.Context, id, network string, rulesets map[int]string, limit int, cursor string) (HistoryDetail, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 500 {
		return HistoryDetail{}, ErrBadQuery
	}
	return s.history(ctx, id, network, rulesets, limit, cursor)
}

func (s *Store) history(ctx context.Context, id, network string, rulesets map[int]string, limit int, cursor string) (HistoryDetail, error) {
	if err := ValidateContractID(id); err != nil {
		return HistoryDetail{}, err
	}
	var after uint32
	if cursor != "" {
		if len(cursor) > 256 {
			return HistoryDetail{}, ErrBadQuery
		}
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return HistoryDetail{}, ErrBadQuery
		}
		var c historyCursor
		if err = json.Unmarshal(b, &c); err != nil || c.Version != 1 || c.ID != id || c.Ledger == 0 {
			return HistoryDetail{}, ErrBadQuery
		}
		after = c.Ledger
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return HistoryDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var currentHash *string
	err = tx.QueryRowContext(ctx, `SELECT current_wasm_hash FROM contracts WHERE contract_id=?`, id).Scan(&currentHash)
	if errors.Is(err, sql.ErrNoRows) {
		return HistoryDetail{}, ErrNotFound
	}
	if err != nil {
		return HistoryDetail{}, err
	}
	d := HistoryDetail{ContractID: id, Network: network, History: []HistoryVersion{}, ParserVersion: sepmeta.ParserVersion}
	d.AsOfLedger, err = readLedger(ctx, tx)
	if err != nil {
		return d, err
	}
	versions, err := versionRange(ctx, tx, id, after, limit+1)
	if err != nil {
		return d, err
	}
	if len(versions) > limit {
		versions = versions[:limit]
		b, _ := json.Marshal(historyCursor{1, id, versions[limit-1].FromLedger})
		c := base64.RawURLEncoding.EncodeToString(b)
		d.NextCursor = &c
	}
	for _, v := range versions {
		claims, err := claimsFor(ctx, tx, id, v.WasmHash, currentHash, v.WasmHash == nil && v.ExecRef == nil, rulesets)
		if err != nil {
			return d, err
		}
		d.History = append(d.History, HistoryVersion{v, claims})
	}
	return d, tx.Commit()
}
