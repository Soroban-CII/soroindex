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
	where := []string{"c.archived = 0", "c.contract_id > ?"}
	args := []any{after}
	if f.Kind != "" {
		where = append(where, "c.kind = ?")
		args = append(args, f.Kind)
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
