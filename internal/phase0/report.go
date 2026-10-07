package phase0

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// Rate is a count out of a population. Pct is for display only.
type Rate struct {
	N   int     `json:"n"`
	Of  int     `json:"of"`
	Pct float64 `json:"pct"`
}

func rate(n, of int) Rate {
	r := Rate{N: n, Of: of}
	if of > 0 {
		r.Pct = 100 * float64(n) / float64(of)
	}
	return r
}

// Interval is a confidence interval in percent.
type Interval struct {
	Low  float64 `json:"low_pct"`
	High float64 `json:"high_pct"`
}

// Wilson returns the 95% Wilson score interval for n successes out of of.
func Wilson(n, of int) Interval {
	if of == 0 {
		return Interval{0, 100}
	}
	const z = 1.959963984540054
	p := float64(n) / float64(of)
	nn := float64(of)
	den := 1 + z*z/nn
	center := (p + z*z/(2*nn)) / den
	half := z * math.Sqrt(p*(1-p)/nn+z*z/(4*nn*nn)) / den
	return Interval{Low: 100 * math.Max(0, center-half), High: 100 * math.Min(1, center+half)}
}

// Pair counts something by unique Wasm hash and by contract.
type Pair struct {
	Hashes    int `json:"hashes"`
	Contracts int `json:"contracts"`
}

// SEPCount is how many Wasm hashes and contracts declare one SEP.
type SEPCount struct {
	SEP int `json:"sep"`
	Pair
}

// AnomalyCount summarizes one anomaly code.
type AnomalyCount struct {
	Code     string   `json:"code"`
	Tokens   int      `json:"tokens"`
	Hashes   int      `json:"hashes"`
	Examples []string `json:"examples"` // up to 3 "raw token" (hash prefix)
}

// Summary is the machine-readable Phase 0 result for one network. Stage D
// checks the stored totals against it.
type Summary struct {
	Network         string         `json:"network"`
	GeneratedAt     string         `json:"generated_at"` // RFC 3339 UTC
	LatestLedger    uint32         `json:"latest_ledger"`
	ParserVersion   string         `json:"parser_version"`
	RulesetVersions []string       `json:"ruleset_versions"`
	Method          string         `json:"method"`
	Limits          sepmeta.Limits `json:"limits"`

	Contracts struct {
		Total                int `json:"total"`
		SAC                  int `json:"sac"`
		Wasm                 int `json:"wasm"`
		WasmRef              int `json:"wasm_ref"`
		WasmRefDistinct      int `json:"wasm_ref_distinct_refs"`
		WasmRefUnresolved    int `json:"wasm_ref_unresolved"`
		ArchivedInstances    int `json:"archived_instances"`
		InstanceDecodeErrors int `json:"instance_decode_errors"`
		// Measured: wasm-running contracts whose code was fetched; the
		// denominator for every by-contract rate.
		Measured int `json:"measured"`
	} `json:"contracts"`

	Hashes struct {
		CodeEntries    int `json:"code_entries"` // rows in the code census (mainnet only)
		CodeDeleted    int `json:"code_deleted"` // of those, deleted
		Population     int `json:"population"`   // hashes measured for
		Archived       int `json:"archived"`     // not fetchable (missing or TTL passed)
		Measured       int `json:"measured"`     // fetched; the by-hash denominator
		ParseOK        int `json:"parse_ok"`
		ParsePartial   int `json:"parse_partial"`
		ParseError     int `json:"parse_error"`
		UsedByContract int `json:"used_by_a_contract"` // measured hashes that some contract runs
	} `json:"hashes"`

	DeclaresAny struct {
		ByHash     Rate      `json:"by_hash"`
		ByContract Rate      `json:"by_contract"`
		Wilson95   *Interval `json:"wilson95_by_contract,omitempty"` // sample networks only
	} `json:"declares_any"`
	WithSpec  Rate           `json:"with_spec_by_hash"`
	PerSEP    []SEPCount     `json:"per_sep"`
	Anomalies []AnomalyCount `json:"anomalies"`

	SEP41 struct {
		Status        map[string]Pair `json:"status"`         // match/partial/mismatch/no_spec
		OKHistogram   []int           `json:"ok_histogram"`   // by hash; index = OK count
		UndeclaredGap Pair            `json:"undeclared_gap"` // match and declares nothing
		Declared41    map[string]Pair `json:"declared_41_by_status"`
	} `json:"sep41"`

	Sample   *SampleStats `json:"sample,omitempty"`
	Decision string       `json:"decision,omitempty"`
}

// Summarize computes the report numbers for a census and its analyzed Wasm.
func Summarize(c Census, results map[string]ingest.WasmResult, population []string, rules []match.RuleFile, lim sepmeta.Limits, generatedAt string) Summary {
	var s Summary
	s.Network, s.GeneratedAt, s.LatestLedger, s.Method, s.Limits = c.Network, generatedAt, c.LatestLedger, c.Method, lim
	s.ParserVersion = sepmeta.ParserVersion
	for _, r := range rules {
		s.RulesetVersions = append(s.RulesetVersions, r.RulesetVersion)
	}

	// Contracts.
	refs := map[[2]string]bool{}
	contractsByHash := map[string]int{}
	s.Contracts.Total = len(c.Contracts)
	s.Contracts.InstanceDecodeErrors = c.InstanceDecodeErrors
	for _, ct := range c.Contracts {
		switch {
		case ct.Archived:
			s.Contracts.ArchivedInstances++
			continue
		case ct.Kind == ingest.KindSAC:
			s.Contracts.SAC++
			continue
		case ct.Kind == ingest.KindWasm:
			s.Contracts.Wasm++
		case ct.Kind == ingest.KindWasmRef:
			s.Contracts.WasmRef++
			refs[[2]string{ct.RefOwner, ct.RefTag}] = true
			if ct.Unresolved {
				s.Contracts.WasmRefUnresolved++
				continue
			}
		}
		if r, ok := results[ct.WasmHash]; ok && r.ParseStatus != ingest.ParseArchived {
			s.Contracts.Measured++
			contractsByHash[ct.WasmHash]++
		}
	}
	s.Contracts.WasmRefDistinct = len(refs)

	// Hashes.
	s.Hashes.CodeEntries = len(c.CodeHashes)
	for _, h := range c.CodeHashes {
		if h.Deleted {
			s.Hashes.CodeDeleted++
		}
	}
	s.Hashes.Population = len(population)
	measured := make([]ingest.WasmResult, 0, len(population))
	for _, h := range population {
		r := results[h]
		switch r.ParseStatus {
		case ingest.ParseArchived:
			s.Hashes.Archived++
			continue
		case ingest.ParseOK:
			s.Hashes.ParseOK++
		case ingest.ParsePartial:
			s.Hashes.ParsePartial++
		case ingest.ParseError:
			s.Hashes.ParseError++
		}
		measured = append(measured, r)
		if contractsByHash[h] > 0 {
			s.Hashes.UsedByContract++
		}
	}
	s.Hashes.Measured = len(measured)

	// Declarations, spec, anomalies: by hash over the population; by
	// contract over measured contracts (which may run hashes outside the
	// population, e.g. deleted code that a live instance still names).
	declHashes, specHashes := 0, 0
	perSEP := map[int]*SEPCount{}
	anom := map[string]*AnomalyCount{}
	for _, r := range measured {
		if len(r.Claims.SEPs) > 0 {
			declHashes++
		}
		if r.HasSpec {
			specHashes++
		}
		for _, sep := range r.Claims.SEPs {
			if perSEP[sep] == nil {
				perSEP[sep] = &SEPCount{SEP: sep}
			}
			perSEP[sep].Hashes++
		}
		hashSeen := map[string]bool{}
		for _, tok := range r.Claims.Tokens {
			if tok.Anomaly == "" {
				continue
			}
			a := anom[tok.Anomaly]
			if a == nil {
				a = &AnomalyCount{Code: tok.Anomaly, Examples: []string{}}
				anom[tok.Anomaly] = a
			}
			a.Tokens++
			if !hashSeen[tok.Anomaly] {
				hashSeen[tok.Anomaly] = true
				a.Hashes++
			}
			if len(a.Examples) < 3 {
				a.Examples = append(a.Examples, fmt.Sprintf("%q (%s…)", tok.Raw, r.Hash[:12]))
			}
		}
	}
	declContracts := 0
	for h, n := range contractsByHash {
		r := results[h]
		if len(r.Claims.SEPs) > 0 {
			declContracts += n
		}
		for _, sep := range r.Claims.SEPs {
			if perSEP[sep] == nil {
				perSEP[sep] = &SEPCount{SEP: sep}
			}
			perSEP[sep].Contracts += n
		}
	}
	s.DeclaresAny.ByHash = rate(declHashes, len(measured))
	s.DeclaresAny.ByContract = rate(declContracts, s.Contracts.Measured)
	s.WithSpec = rate(specHashes, len(measured))
	for _, p := range perSEP {
		s.PerSEP = append(s.PerSEP, *p)
	}
	sort.Slice(s.PerSEP, func(i, j int) bool { return s.PerSEP[i].SEP < s.PerSEP[j].SEP })
	s.PerSEP = nonNilSlice(s.PerSEP)
	for _, a := range anom {
		s.Anomalies = append(s.Anomalies, *a)
	}
	sort.Slice(s.Anomalies, func(i, j int) bool { return s.Anomalies[i].Code < s.Anomalies[j].Code })
	s.Anomalies = nonNilSlice(s.Anomalies)

	// SEP-41 matcher distribution.
	s.SEP41.Status = map[string]Pair{}
	s.SEP41.Declared41 = map[string]Pair{}
	for _, st := range []match.Status{match.StatusMatch, match.StatusPartial, match.StatusMismatch, match.StatusNoSpec} {
		s.SEP41.Status[string(st)] = Pair{}
		s.SEP41.Declared41[string(st)] = Pair{}
	}
	maxOK := 0
	for _, r := range measured {
		if m, ok := r.MatchFor(41); ok && m.RequiredCount > maxOK {
			maxOK = m.RequiredCount
		}
	}
	s.SEP41.OKHistogram = make([]int, maxOK+1)
	bump := func(m map[string]Pair, st match.Status, hashes, contracts int) {
		p := m[string(st)]
		p.Hashes += hashes
		p.Contracts += contracts
		m[string(st)] = p
	}
	for _, r := range measured {
		m, ok := r.MatchFor(41)
		if !ok {
			continue
		}
		n := contractsByHash[r.Hash]
		bump(s.SEP41.Status, m.Status, 1, n)
		if m.Status != match.StatusNoSpec && m.OKCount < len(s.SEP41.OKHistogram) {
			s.SEP41.OKHistogram[m.OKCount]++
		}
		if r.Declares(41) {
			bump(s.SEP41.Declared41, m.Status, 1, n)
		}
		if m.Status == match.StatusMatch && len(r.Claims.SEPs) == 0 {
			s.SEP41.UndeclaredGap.Hashes++
			s.SEP41.UndeclaredGap.Contracts += n
		}
	}
	// Contracts running hashes outside the population still count by contract.
	inPop := map[string]bool{}
	for _, h := range population {
		inPop[h] = true
	}
	for h, n := range contractsByHash {
		if inPop[h] {
			continue
		}
		r := results[h]
		if m, ok := r.MatchFor(41); ok {
			bump(s.SEP41.Status, m.Status, 0, n)
			if r.Declares(41) {
				bump(s.SEP41.Declared41, m.Status, 0, n)
			}
			if m.Status == match.StatusMatch && len(r.Claims.SEPs) == 0 {
				s.SEP41.UndeclaredGap.Contracts += n
			}
		}
	}
	return s
}

func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Decide applies the CLAUDE.md §5.10 decision rule to the mainnet share of
// unique Wasm hashes that declare any SEP.
func Decide(byHashPct float64) string {
	switch {
	case byHashPct >= 5:
		return "≥ 5%: README headline \"Declared index\"; Phase 2 proceeds after stage G."
	case byHashPct >= 1:
		return "1% to < 5%: README headline \"Inferred tier + declared-vs-inferred gap\"; Phase 2 proceeds after stage G."
	default:
		return "< 1%: README headline \"Interface index + adoption tracker\" with outreach in \"How to help\"; Phase 2 deferred."
	}
}

// WriteRawCSV writes one row per Wasm hash in the population (CLAUDE.md
// §5.10: report/<network>-raw.csv).
func WriteRawCSV(w io.Writer, population []string, results map[string]ingest.WasmResult, contractsByHash map[string]int) error {
	cw := csv.NewWriter(w)
	header := []string{"wasm_hash", "size_bytes", "contracts", "parse_status", "parse_error", "has_meta", "has_spec",
		"sep_entry_count", "seps", "anomalies", "sep41_status", "sep41_ok_count", "sep41_missing", "sep41_mismatched",
		"parser_version", "ruleset_version"}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, h := range population {
		r := results[h]
		var seps, anomalies []string
		for _, s := range r.Claims.SEPs {
			seps = append(seps, strconv.Itoa(s))
		}
		for _, t := range r.Claims.Tokens {
			if t.Anomaly != "" {
				anomalies = append(anomalies, t.Anomaly+":"+t.Raw)
			}
		}
		m, _ := r.MatchFor(41)
		var mism []string
		for _, x := range m.Mismatched {
			mism = append(mism, x.Fn)
		}
		row := []string{h, strconv.Itoa(r.Size), strconv.Itoa(contractsByHash[h]), r.ParseStatus, r.ParseError,
			strconv.FormatBool(r.HasMeta), strconv.FormatBool(r.HasSpec), strconv.Itoa(r.Claims.EntryCount),
			strings.Join(seps, ";"), strings.Join(anomalies, ";"), string(m.Status), strconv.Itoa(m.OKCount),
			strings.Join(m.Missing, ";"), strings.Join(mism, ";"), sepmeta.ParserVersion, m.RulesetVersion}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// ContractsByHash counts measured contracts per resolved Wasm hash.
func ContractsByHash(c Census) map[string]int {
	out := map[string]int{}
	for _, ct := range c.Contracts {
		if ct.Archived || ct.Unresolved || ct.Kind == ingest.KindSAC || ct.WasmHash == "" {
			continue
		}
		out[ct.WasmHash]++
	}
	return out
}
