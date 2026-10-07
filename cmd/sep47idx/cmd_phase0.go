package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/phase0"
	"github.com/Soroban-CII/soroindex/internal/rpc"
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
	flags := flag.NewFlagSet("phase0", flag.ContinueOnError)
	flags.SetOutput(stderr)
	resolve := config.RegisterCommon(flags)
	hashes := flags.String("hashes", "", "mainnet: Hubble code-hash CSV (Q1 of scripts/hubble-export.sql)")
	instances := flags.String("instances", "", "mainnet: Hubble instance CSV (Q2)")
	execRefs := flags.String("exec-refs", "", "mainnet: optional Hubble CAP-85 reference CSV (Q3)")
	sample := flags.Int("sample", 2000, "testnet: contracts to sample")
	strata := flags.Int("strata", 20, "testnet: equal ledger windows across the retention window")
	pageSize := flags.Uint("page-size", 10, "testnet: ledgers per getLedgers call (1-200)")
	out := flags.String("out", "report/", "output directory")
	rulesDir := flags.String("rules", "", "load rule files from this directory instead of the embedded set")
	threshold := flags.Float64("partial-threshold", match.DefaultPartialThreshold, "match.partial_threshold: share of required functions for \"partial\"")
	if err := flags.Parse(args); err != nil {
		return exitError
	}
	cfg, err := resolve()
	if err != nil {
		errorf(stderr, "sep47idx phase0: %v\n", err)
		return exitError
	}
	if err := cfg.RequireRPC(); err != nil {
		errorf(stderr, "sep47idx phase0: %v\n", err)
		return exitError
	}
	if *threshold <= 0 || *threshold > 1 || *pageSize < 1 || *pageSize > rpc.MaxLedgersLimit {
		errorf(stderr, "sep47idx phase0: --partial-threshold must be in (0,1] and --page-size in 1..%d\n", rpc.MaxLedgersLimit)
		return exitError
	}

	var rfs fs.FS = rules.FS
	if *rulesDir != "" {
		rfs = os.DirFS(*rulesDir)
	}
	ruleFiles, err := match.LoadRules(rfs)
	if err != nil {
		errorf(stderr, "sep47idx phase0: rules: %v\n", err)
		return exitError
	}

	log := slog.New(slog.NewJSONHandler(stderr, nil))
	client, err := rpc.New(rpc.Config{URL: cfg.RPCURL, Timeout: cfg.RPCTimeout, Concurrency: cfg.Concurrency, Logger: log})
	if err != nil {
		errorf(stderr, "sep47idx phase0: %v\n", err)
		return exitError
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Refuse to measure one network while talking to another.
	n, err := client.GetNetwork(ctx)
	if err != nil {
		errorf(stderr, "sep47idx phase0: getNetwork: %v\n", err)
		return exitError
	}
	if n.Passphrase != cfg.Network.Passphrase {
		errorf(stderr, "sep47idx phase0: RPC %s serves %q, not %s\n", cfg.RPCURL, n.Passphrase, cfg.Network.Name)
		return exitError
	}

	opts := phase0.Options{Rules: ruleFiles, Matcher: match.Matcher{PartialThreshold: *threshold},
		Limits: sepmeta.DefaultLimits(), Log: log, Now: time.Now}
	var res phase0.Result
	switch cfg.Network.Name {
	case config.Mainnet.Name:
		res, err = runMainnetPhase0(ctx, client, *hashes, *instances, *execRefs, opts)
	default:
		res, err = phase0.RunTestnet(ctx, client, phase0.SampleConfig{Target: *sample, Strata: *strata, PageSize: uint32(*pageSize)}, opts) // #nosec G115 -- page-size checked to be 1..200 above
	}
	if err != nil {
		errorf(stderr, "sep47idx phase0: %v\n", err)
		return exitError
	}
	if err := phase0.Write(*out, res); err != nil {
		errorf(stderr, "sep47idx phase0: write report: %v\n", err)
		return exitError
	}
	s := res.Summary
	if _, err := fmt.Fprintf(stdout, "%s: %d contracts (%d SAC, %d wasm, %d wasm_ref); %d hashes fetched, %d archived; declares any SEP: %d/%d hashes (%.2f%%), %d/%d contracts (%.2f%%); SEP-41 match %d hashes; undeclared gap %d hashes\nwrote %s\n",
		s.Network, s.Contracts.Total, s.Contracts.SAC, s.Contracts.Wasm, s.Contracts.WasmRef, s.Hashes.Measured, s.Hashes.Archived,
		s.DeclaresAny.ByHash.N, s.DeclaresAny.ByHash.Of, s.DeclaresAny.ByHash.Pct,
		s.DeclaresAny.ByContract.N, s.DeclaresAny.ByContract.Of, s.DeclaresAny.ByContract.Pct,
		s.SEP41.Status["match"].Hashes, s.SEP41.UndeclaredGap.Hashes, *out); err != nil {
		return exitError
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
