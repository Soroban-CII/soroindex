package phase0

import (
	"fmt"
	"sort"

	"github.com/Soroban-CII/soroindex/internal/store"
)

// ToolchainCensus is the adoption report's view of the Soroban toolchain
// versions recorded in Wasm metadata: the Rust compiler (rsver), the
// soroban-sdk crate (rssdkver) and the Stellar CLI (cliver).
//
// It is recorded from a database seeded from the census population rather
// than from the census push itself, because meta_json is what the index
// stores. Counts cover live instances in that database, so the totals are
// reported alongside the buckets rather than assumed to equal the census
// denominators.
type ToolchainCensus struct {
	MeasuredHashes      int                           `json:"measured_hashes"`
	MeasuredContracts   int                           `json:"measured_contracts"`
	Toolchains          map[string][]ToolchainVersion `json:"toolchains"`
	SEP41RulesetVersion string                        `json:"sep41_ruleset_version,omitempty"`
	ParserVersion       string                        `json:"parser_version"`
	AsOfLedger          uint32                        `json:"as_of_ledger"`
}

// ToolchainVersion is one value of one toolchain key, weighted by unique
// Wasm hash and by contract. Percentages are of the bucket's own hashes or
// contracts.
type ToolchainVersion struct {
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

// ToolchainCensusFromStore converts stored statistics into the report's
// shape. The store keeps one bucket per version; the order is preserved so
// two reports over the same database rank identically.
func ToolchainCensusFromStore(s store.ToolchainStats) ToolchainCensus {
	c := ToolchainCensus{
		MeasuredHashes:    s.MeasuredHashes,
		MeasuredContracts: s.MeasuredContracts,
		Toolchains:        map[string][]ToolchainVersion{},
		ParserVersion:     s.ParserVersion,
		AsOfLedger:        s.AsOfLedger,
	}
	if s.SEP41RulesetVersion != nil {
		c.SEP41RulesetVersion = *s.SEP41RulesetVersion
	}
	for _, key := range store.ToolchainKeys {
		buckets := make([]ToolchainVersion, 0, len(s.Toolchains[key]))
		for _, b := range s.Toolchains[key] {
			buckets = append(buckets, ToolchainVersion{
				Version:                 b.Version,
				Hashes:                  b.Hashes,
				Contracts:               b.Contracts,
				DeclaringHashes:         b.DeclaringHashes,
				DeclaringContracts:      b.DeclaringContracts,
				SEP41MatchHashes:        b.SEP41MatchHashes,
				SEP41MatchContracts:     b.SEP41MatchContracts,
				DeclaringPctByHash:      b.DeclaringPctByHash,
				DeclaringPctByContract:  b.DeclaringPctByContract,
				SEP41MatchPctByHash:     b.SEP41MatchPctByHash,
				SEP41MatchPctByContract: b.SEP41MatchPctByContract,
			})
		}
		sort.SliceStable(buckets, func(i, j int) bool {
			if buckets[i].Hashes != buckets[j].Hashes {
				return buckets[i].Hashes > buckets[j].Hashes
			}
			return buckets[i].Version < buckets[j].Version
		})
		c.Toolchains[key] = buckets
	}
	return c
}

// RecordToolchains attaches stored toolchain statistics to a census summary
// and rebuilds ADOPTION.md. The census itself never reads a database, so
// this is a separate operator step, like --record-instance-archival.
func RecordToolchains(dir string, s Summary, stats store.ToolchainStats) error {
	c := ToolchainCensusFromStore(stats)
	s.Toolchains = &c
	return WriteSummary(dir, s)
}

// Toolchain command hint used when a summary has no recorded census.
func toolchainRecordCommand(network string) string {
	return fmt.Sprintf("sep47idx phase0 --network %s --out report/ --record-toolchains <db>", network)
}
