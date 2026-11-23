package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"unicode/utf8"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// ToolchainKeys are the meta keys the Soroban toolchain writes into every
// contract it builds: the Rust compiler, the soroban-sdk crate and the
// Stellar CLI versions.
var ToolchainKeys = []string{"rsver", "rssdkver", "cliver"}

// Toolchain bucket names for values that are not a single usable version.
const (
	// ToolchainUnknown covers a missing key, an empty value, a value longer
	// than maxToolchainValue bytes, or one with control characters or
	// invalid UTF-8. Meta values are untrusted contract input.
	ToolchainUnknown = "unknown"
	// ToolchainMultiple is a Wasm whose meta gives the key more than one
	// distinct value. Picking one would claim more than the meta says.
	ToolchainMultiple = "multiple"
)

const maxToolchainValue = 64

// ToolchainBucket counts one version of one toolchain key. Counts are
// weighted two ways, like the adoption report: by unique Wasm hash and by
// live contract, because factory deployments inflate per-contract numbers.
// Percentages are of this bucket's own hashes or contracts, rounded to two
// decimals.
type ToolchainBucket struct {
	Version                 string  `json:"version"`
	Hashes                  int     `json:"hashes"`
	Contracts               int     `json:"contracts"`
	DeclaringHashes         int     `json:"declaring_hashes"`
	DeclaringContracts      int     `json:"declaring_contracts"`
	SEP41MatchHashes        int     `json:"sep41_match_hashes"`
	SEP41MatchContracts     int     `json:"sep41_match_contracts"`
	DeclaringPctByHash      float64 `json:"declaring_pct_by_hash"`
	DeclaringPctByContract  float64 `json:"declaring_pct_by_contract"`
	SEP41MatchPctByHash     float64 `json:"sep41_match_pct_by_hash"`
	SEP41MatchPctByContract float64 `json:"sep41_match_pct_by_contract"`
}

// ToolchainStats is the toolchain distribution over measured code: live
// wasm and resolved wasm_ref contracts whose current Wasm was parsed, the
// same population as Totals. "Declaring" means the Wasm declares any SEP;
// "SEP-41 match" is the inferred status under the loaded SEP-41 rules.
type ToolchainStats struct {
	Network             string                       `json:"network"`
	MeasuredHashes      int                          `json:"measured_hashes"`
	MeasuredContracts   int                          `json:"measured_contracts"`
	Toolchains          map[string][]ToolchainBucket `json:"toolchains"`
	SEP41RulesetVersion *string                      `json:"sep41_ruleset_version"`
	ParserVersion       string                       `json:"parser_version"`
	AsOfLedger          uint32                       `json:"as_of_ledger"`
}

// Toolchains reads stored meta_json for every measured Wasm in one snapshot
// and buckets it by toolchain version. It reads one row per unique hash, so
// its cost follows the number of distinct Wasm, not of contracts.
func (s *Store) Toolchains(ctx context.Context, network string, rulesets map[int]string) (ToolchainStats, error) {
	d := ToolchainStats{Network: network, Toolchains: map[string][]ToolchainBucket{}, ParserVersion: sepmeta.ParserVersion}
	sep41 := rulesets[41]
	if sep41 != "" {
		d.SEP41RulesetVersion = &sep41
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return d, err
	}
	defer func() { _ = tx.Rollback() }()
	if d.AsOfLedger, err = readLedger(ctx, tx); err != nil {
		return d, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT w.hash, w.meta_json, count(*),
		EXISTS (SELECT 1 FROM wasm_claims wc WHERE wc.wasm_hash = w.hash),
		EXISTS (SELECT 1 FROM interface_matches im WHERE im.wasm_hash = w.hash AND im.sep = 41 AND im.ruleset_version = ? AND im.status = 'match')
		FROM contracts c JOIN wasm w ON w.hash = c.current_wasm_hash
		WHERE c.archived = 0 AND c.kind IN ('wasm','wasm_ref') AND w.parse_status <> 'archived'
		GROUP BY w.hash ORDER BY w.hash`, sep41)
	if err != nil {
		return d, err
	}
	buckets := map[string]map[string]*ToolchainBucket{}
	for _, k := range ToolchainKeys {
		buckets[k] = map[string]*ToolchainBucket{}
	}
	for rows.Next() {
		var hash string
		var meta sql.NullString
		var contracts int
		var declares, matches bool
		if err = rows.Scan(&hash, &meta, &contracts, &declares, &matches); err != nil {
			break
		}
		var versions map[string]string
		if versions, err = toolchainVersions(meta); err != nil {
			break
		}
		d.MeasuredHashes++
		d.MeasuredContracts += contracts
		for _, k := range ToolchainKeys {
			b := buckets[k][versions[k]]
			if b == nil {
				b = &ToolchainBucket{Version: versions[k]}
				buckets[k][versions[k]] = b
			}
			b.Hashes++
			b.Contracts += contracts
			if declares {
				b.DeclaringHashes++
				b.DeclaringContracts += contracts
			}
			if matches {
				b.SEP41MatchHashes++
				b.SEP41MatchContracts += contracts
			}
		}
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return d, err
	}
	for _, k := range ToolchainKeys {
		out := make([]ToolchainBucket, 0, len(buckets[k]))
		for _, b := range buckets[k] {
			b.DeclaringPctByHash = pct(b.DeclaringHashes, b.Hashes)
			b.DeclaringPctByContract = pct(b.DeclaringContracts, b.Contracts)
			b.SEP41MatchPctByHash = pct(b.SEP41MatchHashes, b.Hashes)
			b.SEP41MatchPctByContract = pct(b.SEP41MatchContracts, b.Contracts)
			out = append(out, *b)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Hashes != out[j].Hashes {
				return out[i].Hashes > out[j].Hashes
			}
			return out[i].Version < out[j].Version
		})
		d.Toolchains[k] = out
	}
	return d, tx.Commit()
}

// toolchainVersions reads one Wasm's stored meta pairs and returns the bucket
// for every toolchain key.
func toolchainVersions(meta sql.NullString) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range ToolchainKeys {
		out[k] = ToolchainUnknown
	}
	if !meta.Valid {
		return out, nil
	}
	var pairs []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(meta.String), &pairs); err != nil {
		return nil, errors.New("invalid stored metadata JSON")
	}
	seen := map[string]map[string]bool{}
	for _, p := range pairs {
		if _, wanted := out[p.Key]; !wanted || p.Value == "" {
			continue
		}
		if seen[p.Key] == nil {
			seen[p.Key] = map[string]bool{}
		}
		seen[p.Key][p.Value] = true
	}
	for k, values := range seen {
		if len(values) > 1 {
			out[k] = ToolchainMultiple
			continue
		}
		for v := range values {
			if usableVersion(v) {
				out[k] = v
			}
		}
	}
	return out, nil
}

func usableVersion(v string) bool {
	if len(v) > maxToolchainValue || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// pct is part as a percentage of whole, rounded to two decimals; 0 when whole is 0.
func pct(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return math.Round(float64(part)*10000/float64(whole)) / 100
}
