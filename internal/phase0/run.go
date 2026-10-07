package phase0

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// Options are shared by both runs.
type Options struct {
	Rules   []match.RuleFile
	Matcher match.Matcher
	Limits  sepmeta.Limits
	Log     *slog.Logger
	Now     func() time.Time
}

// Result is a finished run, ready to write.
type Result struct {
	Summary    Summary
	Census     Census
	Population []string
	Wasm       map[string]ingest.WasmResult
}

// MainnetInputs are the Hubble exports (scripts/hubble-export.sql).
type MainnetInputs struct {
	Hashes    io.Reader // Q1, required
	Instances io.Reader // Q2, required
	ExecRefs  io.Reader // Q3, optional
}

// RunMainnet takes a census from the Hubble exports: every code hash and
// every current contract instance, with each Wasm fetched from RPC.
func RunMainnet(ctx context.Context, c ingest.RPC, in MainnetInputs, o Options) (Result, error) {
	hashes, err := ReadCodeHashes(in.Hashes)
	if err != nil {
		return Result{}, err
	}
	contracts, decodeErrs, err := ReadInstances(in.Instances)
	if err != nil {
		return Result{}, err
	}
	var refs map[[2]string]string
	if in.ExecRefs != nil {
		if refs, err = ReadExecRefs(in.ExecRefs); err != nil {
			return Result{}, err
		}
	}
	if err := ResolveRefs(ctx, c, contracts, refs); err != nil {
		return Result{}, err
	}
	latest, err := c.GetLatestLedger(ctx)
	if err != nil {
		return Result{}, err
	}
	var population []string
	for _, h := range hashes {
		if !h.Deleted {
			population = append(population, h.Hash)
		}
	}
	census := Census{
		Network: "mainnet", Contracts: contracts, CodeHashes: hashes, InstanceDecodeErrors: decodeErrs, LatestLedger: latest.Sequence,
		Method: "Census, not a sample. Contract instances and code hashes come from Stellar's Hubble dataset on BigQuery " +
			"(scripts/hubble-export.sql: current rows of snapshots.contract_data_snapshot and the latest row per hash of " +
			"crypto_stellar.contract_code). Each instance's executable is decoded from its exported XDR; CAP-85 references " +
			"are resolved from the export or with getLedgerEntries. By-hash rates are over code hashes not marked deleted that " +
			"RPC returned live; by-contract rates are over Wasm-running contracts whose code RPC returned live.",
	}
	return finish(ctx, c, census, population, o, nil)
}

// RunTestnet samples contracts from the RPC retention window, because
// Hubble does not cover testnet.
func RunTestnet(ctx context.Context, c ingest.RPC, sc SampleConfig, o Options) (Result, error) {
	ids, st, err := SampleTestnet(ctx, c, sc, o.Log)
	if err != nil {
		return Result{}, err
	}
	contracts, latest, err := CurrentInstances(ctx, c, ids)
	if err != nil {
		return Result{}, err
	}
	if err := ResolveRefs(ctx, c, contracts, nil); err != nil {
		return Result{}, err
	}
	seen := map[string]bool{}
	var population []string
	for _, ct := range contracts {
		if ct.WasmHash != "" && !seen[ct.WasmHash] {
			seen[ct.WasmHash] = true
			population = append(population, ct.WasmHash)
		}
	}
	sort.Strings(population)
	census := Census{
		Network: "testnet", Contracts: contracts, LatestLedger: latest,
		Method: fmt.Sprintf("Sample, not a census: Hubble does not cover testnet. The RPC retention window (ledgers %d–%d) "+
			"was split into %d equal strata; each was scanned from its start with getLedgers until it gave its share of "+
			"contracts not seen before. A contract enters the sample when its instance entry is created or updated in a "+
			"scanned ledger, so active contracts are more likely to be drawn than idle ones. Each sampled contract's current "+
			"instance was then read with getLedgerEntries. By-hash rates are over the distinct hashes the sampled contracts run.",
			st.OldestLedger, st.LatestLedger, st.Strata),
	}
	return finish(ctx, c, census, population, o, &st)
}

func finish(ctx context.Context, c ingest.RPC, census Census, population []string, o Options, st *SampleStats) (Result, error) {
	want := map[string]bool{}
	for _, h := range population {
		want[h] = true
	}
	for _, ct := range census.Contracts {
		if ct.WasmHash != "" {
			want[ct.WasmHash] = true
		}
	}
	all := ingest.SortedKeys(want)
	o.Log.LogAttrs(ctx, slog.LevelInfo, "phase0 fetching code", slog.Int("hashes", len(all)))
	code, err := ingest.FetchCode(ctx, c, all)
	if err != nil {
		return Result{}, err
	}
	results := make(map[string]ingest.WasmResult, len(all))
	for _, h := range all {
		if cd := code[h]; cd.Archived {
			results[h] = ingest.Archived(h, o.Rules)
		} else {
			results[h] = ingest.Analyze(h, cd.Bytes, o.Rules, o.Matcher, o.Limits)
		}
	}
	s := Summarize(census, results, population, o.Rules, o.Limits, o.Now().UTC().Format(time.RFC3339))
	if st != nil {
		s.Sample = st
		w := Wilson(s.DeclaresAny.ByContract.N, s.DeclaresAny.ByContract.Of)
		s.DeclaresAny.Wilson95 = &w
	}
	if census.Network == "mainnet" {
		s.Decision = Decide(s.DeclaresAny.ByHash.Pct)
	}
	return Result{Summary: s, Census: census, Population: population, Wasm: results}, nil
}

// Write stores a run in dir: <network>-raw.csv, <network>-contracts.csv
// and <network>-summary.json, then regenerates ADOPTION.md from every
// summary present.
func Write(dir string, r Result) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	var raw bytes.Buffer
	if err := WriteRawCSV(&raw, r.Population, r.Wasm, ContractsByHash(r.Census)); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, r.Summary.Network+"-raw.csv"), raw.Bytes()); err != nil {
		return err
	}
	var contracts bytes.Buffer
	if err := WriteContractsCSV(&contracts, r.Census); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, r.Summary.Network+"-contracts.csv"), contracts.Bytes()); err != nil {
		return err
	}
	return WriteSummary(dir, r.Summary)
}

// WriteSummary writes dir/<network>-summary.json and rebuilds ADOPTION.md.
func WriteSummary(dir string, s Summary) error {
	js, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, s.Network+"-summary.json"), append(js, '\n')); err != nil {
		return err
	}
	return RegenerateAdoption(dir)
}

// ReadSummary reads dir/<network>-summary.json.
func ReadSummary(dir, network string) (Summary, error) {
	b, err := os.ReadFile(filepath.Join(dir, network+"-summary.json")) // #nosec G304 -- operator-chosen report directory
	if err != nil {
		return Summary{}, err
	}
	var s Summary
	if err := json.Unmarshal(b, &s); err != nil {
		return Summary{}, fmt.Errorf("%s summary: %w", network, err)
	}
	return s, nil
}

// RegenerateAdoption rebuilds dir/ADOPTION.md from dir/*-summary.json.
func RegenerateAdoption(dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*-summary.json"))
	if err != nil {
		return err
	}
	var sums []Summary
	for _, f := range files {
		b, err := os.ReadFile(f) // #nosec G304 -- operator-chosen report directory
		if err != nil {
			return err
		}
		var s Summary
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		sums = append(sums, s)
	}
	return writeFile(filepath.Join(dir, "ADOPTION.md"), []byte(RenderAdoption(sums)))
}

// writeFile writes atomically: a crash leaves the old file, not half a file.
func writeFile(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
