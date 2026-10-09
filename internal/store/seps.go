package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// SEPCounts counts distinct live contracts independently in each trust tier.
type SEPCounts struct {
	SEP      int `json:"sep"`
	Declared int `json:"declared"`
	Inferred int `json:"inferred"`
	Verified int `json:"verified"`
	Protocol int `json:"protocol"`
}

// SEPPage carries the versions used to compute inferred counts.
type SEPPage struct {
	SEPs            []SEPCounts    `json:"seps"`
	NextCursor      *string        `json:"next_cursor"`
	RulesetVersions map[int]string `json:"ruleset_versions"`
	ParserVersion   string         `json:"parser_version"`
	AsOfLedger      uint32         `json:"as_of_ledger"`
}

type sepCursor struct {
	Version int `json:"v"`
	SEP     int `json:"sep"`
}

// SEPs returns a keyset page of tier counts, excluding stale verification runs.
func (s *Store) SEPs(ctx context.Context, limit int, cursor string, rulesets map[int]string) (SEPPage, error) {
	d := SEPPage{SEPs: []SEPCounts{}, RulesetVersions: rulesets, ParserVersion: sepmeta.ParserVersion}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 500 {
		return d, ErrBadQuery
	}
	after := 0
	if cursor != "" {
		if len(cursor) > 128 {
			return d, ErrBadQuery
		}
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return d, ErrBadQuery
		}
		var c sepCursor
		if err = json.Unmarshal(b, &c); err != nil || c.Version != 1 || c.SEP < 1 || c.SEP > 999999 {
			return d, ErrBadQuery
		}
		after = c.SEP
	}
	versions, err := json.Marshal(rulesets)
	if err != nil {
		return d, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return d, err
	}
	defer func() { _ = tx.Rollback() }()
	d.AsOfLedger, err = readLedger(ctx, tx)
	if err != nil {
		return d, err
	}
	rows, err := tx.QueryContext(ctx, `WITH evidence AS (
	 SELECT DISTINCT wc.sep,c.contract_id,'declared' AS tier FROM contracts c JOIN wasm_claims wc ON wc.wasm_hash=c.current_wasm_hash WHERE c.archived=0 AND c.kind<>'sac'
	 UNION ALL
	 SELECT DISTINCT im.sep,c.contract_id,'inferred' FROM contracts c JOIN interface_matches im ON im.wasm_hash=c.current_wasm_hash WHERE c.archived=0 AND c.kind<>'sac' AND im.status='match' AND im.ruleset_version=json_extract(?, '$.' || im.sep)
	 UNION ALL
	 SELECT DISTINCT v.sep,c.contract_id,'verified' FROM contracts c JOIN verifications v ON v.contract_id=c.contract_id AND v.wasm_hash=c.current_wasm_hash WHERE c.archived=0 AND c.kind<>'sac' AND v.passed=1 AND v.verdict='pass' AND EXISTS (SELECT 1 FROM wasm_claims wc WHERE wc.wasm_hash=v.wasm_hash AND wc.sep=v.sep) AND NOT EXISTS (SELECT 1 FROM verifications newer WHERE newer.contract_id=v.contract_id AND newer.wasm_hash=v.wasm_hash AND newer.sep=v.sep AND newer.run_at>v.run_at)
	 UNION ALL
	 SELECT 41,c.contract_id,'protocol' FROM contracts c WHERE c.archived=0 AND c.kind='sac'
	) SELECT sep,sum(tier='declared'),sum(tier='inferred'),sum(tier='verified'),sum(tier='protocol') FROM evidence WHERE sep>? GROUP BY sep ORDER BY sep LIMIT ?`, string(versions), after, limit+1)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var c SEPCounts
		if err = rows.Scan(&c.SEP, &c.Declared, &c.Inferred, &c.Verified, &c.Protocol); err != nil {
			break
		}
		d.SEPs = append(d.SEPs, c)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return d, err
	}
	if len(d.SEPs) > limit {
		d.SEPs = d.SEPs[:limit]
		b, _ := json.Marshal(sepCursor{1, d.SEPs[limit-1].SEP})
		c := base64.RawURLEncoding.EncodeToString(b)
		d.NextCursor = &c
	}
	return d, tx.Commit()
}
