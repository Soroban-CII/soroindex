package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Soroban-CII/soroindex/internal/clierr"
	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/phase0"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
)

func init() {
	commands["phase0"] = command{
		summary: "measure SEP-47 adoption and write the adoption report",
		run:     runPhase0,
	}
}

// runPhase0 implements CLAUDE.md §5.10:
//
//	sep47idx phase0 --network mainnet --hashes report/mainnet_hashes.csv --instances report/mainnet_instances.csv --out report/
//	sep47idx phase0 --network testnet --sample 2000 --out report/
func runPhase0(args []string, stdout, stderr io.Writer) int {
	jsonMode := jsonRequested(args)
	flags := newFlagSet("phase0", stderr, jsonMode)
	resolve := config.RegisterCommon(flags)
	hashes := flags.String("hashes", "", "mainnet: Hubble code-hash CSV (Q1 of scripts/hubble-export.sql)")
	instances := flags.String("instances", "", "mainnet: Hubble instance CSV (Q2)")
	execRefs := flags.String("exec-refs", "", "mainnet: optional Hubble CAP-85 reference CSV (Q3)")
	sample := flags.Int("sample", 2000, "testnet: contracts to sample")
	strata := flags.Int("strata", 20, "testnet: equal ledger windows across the retention window")
	pageSize := flags.Uint("page-size", 10, "testnet: ledgers per getLedgers call (1-200)")
	out := flags.String("out", "report/", "output directory")
	rulesDir := flags.String("rules", "", "load rule files from this directory instead of the embedded set")
	verifyDB := flags.String("verify-db", "", "compare <out>/<network>-summary.json with a database seeded from <network>-contracts.csv; exit 1 on any difference")
	archivalDB := flags.String("record-instance-archival", "", "record instance archival measured in a database seeded from <network>-contracts.csv into the summary, then rebuild ADOPTION.md")
	toolchainsDB := flags.String("record-toolchains", "", "record the rsver/rssdkver/cliver census from a database seeded from <network>-contracts.csv into the summary, then rebuild ADOPTION.md")
	renderOnly := flags.Bool("render-only", false, "only rebuild <out>/ADOPTION.md from the *-summary.json files already there")
	threshold := flags.Float64("partial-threshold", match.DefaultPartialThreshold, "match.partial_threshold: share of required functions for \"partial\"")
	topHashes := flags.Int("top-hashes", phase0.DefaultTopHashes, "rank this many Wasm hashes by contract count in ADOPTION.md, 1..100")
	if err := parse(flags, args); err != nil {
		return readExit("phase0", err, stderr, jsonMode)
	}
	if *renderOnly {
		if err := phase0.RegenerateAdoption(*out); err != nil {
			return fail(stderr, jsonMode, "phase0", fmt.Errorf("render: %w", err))
		}
		return exitOK
	}
	cfg, err := resolve()
	if err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	if *verifyDB != "" {
		return verifyPhase0DB(cfg, *out, *verifyDB, stdout, stderr, jsonMode)
	}
	if *archivalDB != "" {
		return recordInstanceArchival(cfg, *out, *archivalDB, stdout, stderr, jsonMode)
	}
	if *toolchainsDB != "" {
		return recordToolchains(cfg, *out, *toolchainsDB, stdout, stderr, jsonMode)
	}
	if err := cfg.RequireRPC(); err != nil {
		return fail(stderr, jsonMode, "phase0", clierr.Newf("bad_request", "%v", err))
	}
	if *threshold <= 0 || *threshold > 1 || *pageSize < 1 || *pageSize > rpc.MaxLedgersLimit || *topHashes < 1 || *topHashes > 100 {
		return fail(stderr, jsonMode, "phase0", clierr.Newf("bad_request", "--partial-threshold must be in (0,1], --page-size in 1..%d, and --top-hashes in 1..100", rpc.MaxLedgersLimit))
	}

	var rfs fs.FS = rules.FS
	if *rulesDir != "" {
		rfs = os.DirFS(*rulesDir)
	}
	ruleFiles, err := match.LoadRules(rfs)
	if err != nil {
		return fail(stderr, jsonMode, "phase0", fmt.Errorf("rules: %w", err))
	}

	log := slog.New(slog.NewJSONHandler(stderr, nil))
	client, err := rpc.New(rpc.Config{URL: cfg.RPCURL, Timeout: cfg.RPCTimeout, Concurrency: cfg.Concurrency, Logger: log})
	if err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Refuse to measure one network while talking to another.
	n, err := client.GetNetwork(ctx)
	if err != nil {
		return fail(stderr, jsonMode, "phase0", fmt.Errorf("getNetwork: %w", err))
	}
	if n.Passphrase != cfg.Network.Passphrase {
		return fail(stderr, jsonMode, "phase0", clierr.Newf("network_mismatch", "RPC %s serves %q, not %s", cfg.RPCURL, n.Passphrase, cfg.Network.Name))
	}

	opts := phase0.Options{Rules: ruleFiles, Matcher: match.Matcher{PartialThreshold: *threshold},
		Limits: sepmeta.DefaultLimits(), TopHashes: *topHashes, Log: log, Now: time.Now}
	var res phase0.Result
	switch cfg.Network.Name {
	case config.Mainnet.Name:
		res, err = runMainnetPhase0(ctx, client, *hashes, *instances, *execRefs, opts)
	default:
		res, err = phase0.RunTestnet(ctx, client, phase0.SampleConfig{Target: *sample, Strata: *strata, PageSize: uint32(*pageSize)}, opts) // #nosec G115 -- page-size checked to be 1..200 above
	}
	if err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	if err := phase0.Write(*out, res); err != nil {
		return fail(stderr, jsonMode, "phase0", fmt.Errorf("write report: %w", err))
	}
	s := res.Summary
	if _, err := fmt.Fprintf(stdout, "%s: %d contracts (%d SAC, %d wasm, %d wasm_ref); %d hashes fetched, %d archived; declares any SEP: %d/%d hashes (%.2f%%), %d/%d contracts (%.2f%%); SEP-41 match %d hashes; undeclared gap %d hashes\nwrote %s\n",
		s.Network, s.Contracts.Total, s.Contracts.SAC, s.Contracts.Wasm, s.Contracts.WasmRef, s.Hashes.Measured, s.Hashes.Archived,
		s.DeclaresAny.ByHash.N, s.DeclaresAny.ByHash.Of, s.DeclaresAny.ByHash.Pct,
		s.DeclaresAny.ByContract.N, s.DeclaresAny.ByContract.Of, s.DeclaresAny.ByContract.Pct,
		s.SEP41.Status["match"].Hashes, s.SEP41.UndeclaredGap.Hashes, *out); err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	return exitOK
}

func runMainnetPhase0(ctx context.Context, c *rpc.Client, hashes, instances, execRefs string, o phase0.Options) (phase0.Result, error) {
	if hashes == "" || instances == "" {
		return phase0.Result{}, errors.New("mainnet needs --hashes and --instances (export them with scripts/hubble-export.sql)")
	}
	open := func(p string) (*os.File, error) { return os.Open(p) } // #nosec G304 -- operator-supplied input path
	hf, err := open(hashes)
	if err != nil {
		return phase0.Result{}, err
	}
	defer func() { _ = hf.Close() }() // read-only; close error carries nothing
	inf, err := open(instances)
	if err != nil {
		return phase0.Result{}, err
	}
	defer func() { _ = inf.Close() }() // read-only; close error carries nothing
	in := phase0.MainnetInputs{Hashes: hf, Instances: inf}
	if execRefs != "" {
		rf, err := open(execRefs)
		if err != nil {
			return phase0.Result{}, err
		}
		defer func() { _ = rf.Close() }() // read-only; close error carries nothing
		in.ExecRefs = rf
	}
	return phase0.RunMainnet(ctx, c, in, o)
}

// verifyPhase0DB implements --verify-db (CLAUDE.md §7 step 20).
func verifyPhase0DB(cfg config.Common, out, dbPath string, stdout, stderr io.Writer, jsonMode bool) int {
	b, err := os.ReadFile(filepath.Join(out, cfg.Network.Name+"-summary.json")) // #nosec G304 -- operator-chosen report directory
	if err != nil {
		return fail(stderr, jsonMode, "phase0", clierr.Newf("bad_request", "%v", err))
	}
	var s phase0.Summary
	if err := json.Unmarshal(b, &s); err != nil {
		return fail(stderr, jsonMode, "phase0", clierr.Newf("bad_request", "summary: %v", err))
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dbPath, store.Options{Passphrase: cfg.Network.Passphrase, ReadOnly: true})
	if err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	defer func() { _ = st.Close() }() // read-only
	ruleset := ""
	for _, v := range s.RulesetVersions {
		if strings.HasPrefix(v, "sep41-") {
			ruleset = v
		}
	}
	t, err := st.Totals(ctx, ruleset, !phase0.InstanceArchivalChecked(s))
	if err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	ok, err := phase0.PrintChecks(stdout, phase0.Verify(s, t))
	if err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	if !ok {
		return fail(stderr, jsonMode, "phase0", clierr.Newf("internal", "phase0 summary and database disagree"))
	}
	return exitOK
}

// recordToolchains implements --record-toolchains: read the stored meta of a
// database seeded from the census population and add the toolchain
// distribution to the summary, then rebuild ADOPTION.md.
func recordToolchains(cfg config.Common, out, dbPath string, stdout, stderr io.Writer, jsonMode bool) int {
	s, err := phase0.ReadSummary(out, cfg.Network.Name)
	if err != nil {
		return fail(stderr, jsonMode, "phase0", clierr.Newf("bad_request", "%v", err))
	}
	if s.Toolchains != nil {
		return fail(stderr, jsonMode, "phase0", clierr.Newf("bad_request", "the %s summary already records a toolchain census", cfg.Network.Name))
	}
	rulesets, err := loadedRulesets()
	if err != nil {
		return fail(stderr, jsonMode, "phase0", fmt.Errorf("rules: %w", err))
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dbPath, store.Options{Passphrase: cfg.Network.Passphrase, ReadOnly: true})
	if err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	defer func() { _ = st.Close() }() // read-only
	stats, err := st.Toolchains(ctx, cfg.Network.Name, rulesets)
	if err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	if stats.MeasuredHashes == 0 {
		return fail(stderr, jsonMode, "phase0", clierr.Newf("bad_request", "database holds no measured Wasm; seed it from %s-contracts.csv first", cfg.Network.Name))
	}
	if err := phase0.RecordToolchains(out, s, stats); err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	if _, err := fmt.Fprintf(stdout, "%s: %d hashes / %d contracts carry toolchain meta as of ledger %d; ADOPTION.md rebuilt\n",
		cfg.Network.Name, stats.MeasuredHashes, stats.MeasuredContracts, stats.AsOfLedger); err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	return exitOK
}

// recordInstanceArchival implements --record-instance-archival: for a
// census whose source has no instance TTLs, record the archival a seed of
// the same population measured, with its ledger, and rebuild ADOPTION.md.
func recordInstanceArchival(cfg config.Common, out, dbPath string, stdout, stderr io.Writer, jsonMode bool) int {
	s, err := phase0.ReadSummary(out, cfg.Network.Name)
	if err != nil {
		return fail(stderr, jsonMode, "phase0", clierr.Newf("bad_request", "%v", err))
	}
	if phase0.InstanceArchivalChecked(s) {
		return fail(stderr, jsonMode, "phase0", clierr.Newf("bad_request", "the %s census already measured instance archival itself", cfg.Network.Name))
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dbPath, store.Options{Passphrase: cfg.Network.Passphrase, ReadOnly: true})
	if err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	defer func() { _ = st.Close() }() // read-only
	archived, total, err := st.InstanceArchivalCounts(ctx)
	if err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	if total != s.Contracts.Total {
		return fail(stderr, jsonMode, "phase0", clierr.Newf("bad_request", "database holds %d contracts, census %d: seed it from %s-contracts.csv first", total, s.Contracts.Total, cfg.Network.Name))
	}
	last, err := st.State(ctx, store.KeyLastLedger)
	if err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	var ledger uint32
	if _, err := fmt.Sscan(last, &ledger); err != nil {
		return fail(stderr, jsonMode, "phase0", fmt.Errorf("last_ledger %q: %w", last, err))
	}
	s.InstanceArchival = &phase0.InstanceArchival{Archived: archived, Of: total, Ledger: ledger, Source: "seeding the census population"}
	if err := phase0.WriteSummary(out, s); err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	if _, err := fmt.Fprintf(stdout, "%s: %d of %d instances archived as of ledger %d; ADOPTION.md rebuilt\n", cfg.Network.Name, archived, total, ledger); err != nil {
		return fail(stderr, jsonMode, "phase0", err)
	}
	return exitOK
}
