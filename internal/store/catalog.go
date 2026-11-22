package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/stellar/go-stellar-sdk/strkey"
)

// Filter selects current contracts with distinct AND and OR SEP predicates.
type Filter struct {
	Implements    []int
	ImplementsAny []int
	Tier          string
	Kind          string
	Archived      *bool  // nil = only live (archived=0), true = only archived, false = only live
	ExecRefOwner  string // filter by CAP-85 executable reference owner contract ID
	Limit         int
	Cursor        string
	// Rulesets pins inferred results to the loaded rules, not obsolete versions.
	Rulesets map[int]string
}

// ContractSummary is the current executable of one contract.
type ContractSummary struct {
	ID       string     `json:"contract_id"`
	Kind     string     `json:"kind"`
	WasmHash *string    `json:"wasm_hash"`
	ExecRef  *Reference `json:"exec_ref"`
	Archived bool       `json:"archived"`
}

// Reference identifies the CAP-85 executable owner and tag.
type Reference struct {
	Owner string `json:"owner"`
	Tag   string `json:"tag"`
}

// Page uses an opaque cursor to continue after the last returned contract.
type Page struct {
	Contracts       []ContractSummary `json:"contracts"`
	NextCursor      *string           `json:"next_cursor"`
	ParserVersion   string            `json:"parser_version"`
	Tier            string            `json:"tier"`
	RulesetVersions map[int]string    `json:"ruleset_versions,omitempty"`
}

// ErrBadQuery identifies invalid public query input without exposing SQL errors.
var ErrBadQuery = errors.New("invalid query")

// ValidateContractID requires a full C... strkey before querying the database.
func ValidateContractID(id string) error {
	b, err := strkey.Decode(strkey.VersionByteContract, id)
	if err != nil || len(b) != 32 {
		return fmt.Errorf("%w: invalid contract ID", ErrBadQuery)
	}
	return nil
}

// ParseSEPs accepts a comma list of positive decimal SEP numbers.
func ParseSEPs(s string) ([]int, error) {
	out := []int{}
	seen := map[int]bool{}
	if s == "" {
		return out, nil
	}
	if len(s) > 4096 {
		return nil, ErrBadQuery
	}
	for _, part := range strings.Split(s, ",") {
		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 || n > 999999 || strconv.Itoa(n) != part {
			return nil, fmt.Errorf("%w: invalid SEP list", ErrBadQuery)
		}
		if !seen[n] {
			out = append(out, n)
			seen[n] = true
		}
	}
	if len(out) > 100 {
		return nil, ErrBadQuery
	}
	return out, nil
}

// Contracts queries live current contracts. Historical and stale verifications
// never satisfy filters, and protocol SACs never satisfy the declared tier.
func (s *Store) Contracts(ctx context.Context, f Filter) (Page, error) {
	page := Page{Contracts: []ContractSummary{}, ParserVersion: sepmeta.ParserVersion}
	if len(f.Implements) > 100 || len(f.ImplementsAny) > 100 {
		return page, ErrBadQuery
	}
	if f.Tier == "" {
		f.Tier = "declared"
	}
	page.Tier = f.Tier
	if f.Tier == "inferred" {
		page.RulesetVersions = f.Rulesets
	}
	if f.Limit == 0 {
		f.Limit = 50
	}
	if f.Limit < 1 || f.Limit > 500 {
		return page, ErrBadQuery
	}
	if f.Tier != "declared" && f.Tier != "inferred" && f.Tier != "verified" && f.Tier != "protocol" {
		return page, ErrBadQuery
	}
	if f.Kind != "" && f.Kind != "wasm" && f.Kind != "wasm_ref" && f.Kind != "sac" {
		return page, ErrBadQuery
	}
	if f.ExecRefOwner != "" {
		if err := ValidateContractID(f.ExecRefOwner); err != nil {
			return page, ErrBadQuery
		}
	}
	after := ""
	if f.Cursor != "" {
		if len(f.Cursor) > 256 {
			return page, ErrBadQuery
		}
		data, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		if err != nil {
			return page, ErrBadQuery
		}
		var cursor struct {
			Version int    `json:"v"`
			ID      string `json:"id"`
		}
		if err := json.Unmarshal(data, &cursor); err != nil || cursor.Version != 1 || ValidateContractID(cursor.ID) != nil {
			return page, ErrBadQuery
		}
		after = cursor.ID
	}
	where := []string{"c.contract_id > ?"}
	args := []any{after}
	// Handle archived filter: nil/false = live only (archived=0), true = archived only (archived=1)
	if f.Archived == nil || !*f.Archived {
		where = append(where, "c.archived = 0")
	} else {
		where = append(where, "c.archived = 1")
	}
	if f.Kind != "" {
		where = append(where, "c.kind = ?")
		args = append(args, f.Kind)
	}
	if f.ExecRefOwner != "" {
		where = append(where, "c.exec_ref_owner = ?")
		args = append(args, f.ExecRefOwner)
	}
	predicate := func(sep int) (string, []any, error) {
		if sep < 0 || sep > 999999 {
			return "", nil, ErrBadQuery
		}
		switch f.Tier {
		case "protocol":
			if sep != 0 && sep != 41 {
				return "0", nil, nil
			}
			return "c.kind = 'sac'", nil, nil
		case "declared":
			q := `c.kind <> 'sac' AND EXISTS (SELECT 1 FROM wasm_claims wc WHERE wc.wasm_hash = c.current_wasm_hash`
			a := []any{}
			if sep != 0 {
				q += ` AND wc.sep = ?`
				a = append(a, sep)
			}
			return q + `)`, a, nil
		case "inferred":
			// Rulesets are bound as JSON, including an empty set when no rule exists.
			versions, err := json.Marshal(f.Rulesets)
			if err != nil {
				return "", nil, err
			}
			q := `c.kind <> 'sac' AND EXISTS (SELECT 1 FROM interface_matches im WHERE im.wasm_hash = c.current_wasm_hash AND im.status = 'match' AND im.ruleset_version = json_extract(?, '$.' || im.sep)`
			a := []any{string(versions)}
			if sep != 0 {
				q += ` AND im.sep = ?`
				a = append(a, sep)
			}
			return q + `)`, a, nil
		default:
			q := `c.kind <> 'sac' AND EXISTS (SELECT 1 FROM verifications v WHERE v.contract_id = c.contract_id AND v.wasm_hash = c.current_wasm_hash AND v.passed = 1 AND v.verdict = 'pass' AND EXISTS (SELECT 1 FROM wasm_claims wc WHERE wc.wasm_hash = v.wasm_hash AND wc.sep = v.sep) AND NOT EXISTS (SELECT 1 FROM verifications newer WHERE newer.contract_id = v.contract_id AND newer.wasm_hash = v.wasm_hash AND newer.sep = v.sep AND newer.run_at > v.run_at)`
			a := []any{}
			if sep != 0 {
				q += ` AND v.sep = ?`
				a = append(a, sep)
			}
			return q + `)`, a, nil
		}
	}
	for _, sep := range f.Implements {
		if sep <= 0 {
			return page, ErrBadQuery
		}
		q, a, err := predicate(sep)
		if err != nil {
			return page, err
		}
		where = append(where, "("+q+")")
		args = append(args, a...)
	}
	if len(f.ImplementsAny) > 0 {
		qs := []string{}
		for _, sep := range f.ImplementsAny {
			if sep <= 0 {
				return page, ErrBadQuery
			}
			q, a, err := predicate(sep)
			if err != nil {
				return page, err
			}
			qs = append(qs, "("+q+")")
			args = append(args, a...)
		}
		where = append(where, "("+strings.Join(qs, " OR ")+")")
	}
	if len(f.Implements) == 0 && len(f.ImplementsAny) == 0 {
		q, a, err := predicate(0)
		if err != nil {
			return page, err
		}
		where = append(where, "("+q+")")
		args = append(args, a...)
	}
	args = append(args, f.Limit+1)
	// SQL fragments above are fixed predicates; every public value is bound.
	rows, err := s.DB.QueryContext(ctx, `SELECT c.contract_id,c.kind,c.current_wasm_hash,c.exec_ref_owner,c.exec_ref_tag,c.archived FROM contracts c WHERE `+strings.Join(where, " AND ")+` ORDER BY c.contract_id LIMIT ?`, args...) // #nosec G202 -- fixed fragments, bound data
	if err != nil {
		return page, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c ContractSummary
		var owner, tag sql.NullString
		if err := rows.Scan(&c.ID, &c.Kind, &c.WasmHash, &owner, &tag, &c.Archived); err != nil {
			return page, err
		}
		if c.Kind == "wasm_ref" {
			c.ExecRef = &Reference{owner.String, tag.String}
		}
		page.Contracts = append(page.Contracts, c)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Contracts) > f.Limit {
		page.Contracts = page.Contracts[:f.Limit]
		b, err := json.Marshal(struct {
			Version int    `json:"v"`
			ID      string `json:"id"`
		}{1, page.Contracts[f.Limit-1].ID})
		if err != nil {
			return page, err
		}
		cursor := base64.RawURLEncoding.EncodeToString(b)
		page.NextCursor = &cursor
	}
	return page, nil
}

// MaxLookupIDs caps one bulk lookup. A wallet holding more tokens pages
// through them in several requests.
const MaxLookupIDs = 100

// Lookup is the bulk answer for a list of contract IDs. Found contracts keep
// the requested order and the ContractSummary shape of /v1/contracts. IDs the
// index does not know are listed in NotFound, never dropped. Archived
// contracts are returned with archived set, because a caller asking about a
// specific contract should learn it is archived rather than that it is unknown.
type Lookup struct {
	Contracts     []ContractSummary `json:"contracts"`
	NotFound      []string          `json:"not_found"`
	ParserVersion string            `json:"parser_version"`
	AsOfLedger    uint32            `json:"as_of_ledger"`
}

// ParseContractIDs splits a comma list and validates every entry as a contract
// strkey before anything is queried. Duplicates collapse to their first
// position. The error names the 1-based position of the first invalid entry.
func ParseContractIDs(s string) ([]string, error) {
	if s == "" {
		return nil, fmt.Errorf("%w: ids is empty", ErrBadQuery)
	}
	parts := strings.Split(s, ",")
	if len(parts) > MaxLookupIDs {
		return nil, fmt.Errorf("%w: at most %d ids per request", ErrBadQuery, MaxLookupIDs)
	}
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for i, id := range parts {
		if ValidateContractID(id) != nil {
			return nil, fmt.Errorf("%w: ids entry %d is not a contract ID", ErrBadQuery, i+1)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// LookupContracts reads the summaries of ids in one snapshot. Callers pass
// IDs from ParseContractIDs; they are validated again here so the store never
// queries with an unchecked value.
func (s *Store) LookupContracts(ctx context.Context, ids []string) (Lookup, error) {
	l := Lookup{Contracts: []ContractSummary{}, NotFound: []string{}, ParserVersion: sepmeta.ParserVersion}
	if len(ids) == 0 || len(ids) > MaxLookupIDs {
		return l, ErrBadQuery
	}
	for _, id := range ids {
		if err := ValidateContractID(id); err != nil {
			return l, err
		}
	}
	list, err := json.Marshal(ids)
	if err != nil {
		return l, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return l, err
	}
	defer func() { _ = tx.Rollback() }()
	if l.AsOfLedger, err = readLedger(ctx, tx); err != nil {
		return l, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.contract_id,c.kind,c.current_wasm_hash,c.exec_ref_owner,c.exec_ref_tag,c.archived
		FROM contracts c WHERE c.contract_id IN (SELECT value FROM json_each(?))`, string(list))
	if err != nil {
		return l, err
	}
	found := map[string]ContractSummary{}
	for rows.Next() {
		var c ContractSummary
		var owner, tag sql.NullString
		if err = rows.Scan(&c.ID, &c.Kind, &c.WasmHash, &owner, &tag, &c.Archived); err != nil {
			break
		}
		if c.Kind == "wasm_ref" {
			c.ExecRef = &Reference{owner.String, tag.String}
		}
		found[c.ID] = c
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return l, err
	}
	for _, id := range ids {
		if c, ok := found[id]; ok {
			l.Contracts = append(l.Contracts, c)
		} else {
			l.NotFound = append(l.NotFound, id)
		}
	}
	return l, tx.Commit()
}

// VersionChange represents a contract version upgrade event for SSE streaming.
type VersionChange struct {
	ContractID   string     `json:"contract_id"`
	PreviousHash *string    `json:"previous_wasm_hash"`
	NewHash      *string    `json:"new_wasm_hash"`
	Ledger       uint32     `json:"ledger"`
	Kind         string     `json:"kind"`
	ExecRef      *Reference `json:"exec_ref"`
}

// VersionChanges returns all contract version changes (upgrades) with
// from_ledger > after, ordered by ledger. This is used for SSE streaming.
// A bounded window is enforced to limit replay size.
func (s *Store) VersionChanges(ctx context.Context, after uint32, limit int) ([]VersionChange, error) {
	if limit == 0 {
		limit = 1000
	}
	if limit < 1 || limit > 10000 {
		return nil, ErrBadQuery
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT cv.contract_id, cv.wasm_hash, cv.from_ledger, cv.to_ledger, cv.exec_ref_owner, cv.exec_ref_tag, c.kind
		FROM contract_versions cv
		JOIN contracts c ON c.contract_id = cv.contract_id
		WHERE cv.from_ledger > ?
		ORDER BY cv.from_ledger, cv.contract_id
		LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var changes []VersionChange
	var prevHashMap = map[string]*string{} // track previous hash per contract

	for rows.Next() {
		var v VersionChange
		var hash sql.NullString
		var from, to sql.NullInt64
		var owner, tag, kind sql.NullString
		if err := rows.Scan(&v.ContractID, &hash, &from, &to, &owner, &tag, &kind); err != nil {
			return nil, err
		}
		if hash.Valid {
			v.NewHash = &hash.String
		}
		if from.Valid {
			v.Ledger, err = toLedger(from.Int64)
			if err != nil {
				return nil, err
			}
		}
		if kind.Valid {
			v.Kind = kind.String
		}
		if owner.Valid && tag.Valid {
			v.ExecRef = &Reference{owner.String, tag.String}
		}

		// Determine previous hash: if this contract had a prior version in this
		// result set, use its new hash; otherwise look up the current version
		// before the window. For simplicity, we use the to_ledger of the
		// previous version row if present, but since we're ordered by from_ledger,
		// we can track the last seen new hash for this contract.
		if prev, ok := prevHashMap[v.ContractID]; ok {
			v.PreviousHash = prev
		}
		if v.NewHash != nil {
			prevHashMap[v.ContractID] = v.NewHash
		}

		changes = append(changes, v)
	}
	return changes, rows.Err()
}
