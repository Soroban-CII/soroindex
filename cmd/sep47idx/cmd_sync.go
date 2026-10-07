package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Soroban-CII/soroindex/internal/claims"
	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
)

func init() {
	commands["sync"] = command{
		summary: "seed the index from a contract list (incremental sync: stage F)",
		run:     runSync,
	}
}

// runSync implements the seed path of CLAUDE.md §5.9:
//
//	sep47idx sync --network <n> --seed <file>
func runSync(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sync", flag.ContinueOnError)
	flags.SetOutput(stderr)
	resolve := config.RegisterCommon(flags)
	seed := flags.String("seed", "", "CSV of contract_id[,wasm_hash] to seed from (e.g. the Hubble Q2 export)")
	allowInvocation := flags.Bool("allow-invocation", false, "run claim sources that simulate contract calls (none ship by default)")
	threshold := flags.Float64("partial-threshold", match.DefaultPartialThreshold, "match.partial_threshold")
	if err := flags.Parse(args); err != nil {
		return exitError
	}
	cfg, err := resolve()
	if err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	if *seed == "" {
		errorf(stderr, "sep47idx sync: --seed is required (incremental sync from --start-ledger arrives with stage F)\n")
		return exitError
	}
	if err := cfg.RequireRPC(); err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	log := slog.New(slog.NewJSONHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client, err := rpc.New(rpc.Config{URL: cfg.RPCURL, Timeout: cfg.RPCTimeout, Concurrency: cfg.Concurrency, Logger: log})
	if err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	n, err := client.GetNetwork(ctx)
	if err != nil {
		errorf(stderr, "sep47idx sync: getNetwork: %v\n", err)
		return exitError
	}
	if n.Passphrase != cfg.Network.Passphrase {
		errorf(stderr, "sep47idx sync: RPC %s serves %q, not %s\n", cfg.RPCURL, n.Passphrase, cfg.Network.Name)
		return exitError
	}
	ruleFiles, err := match.LoadRules(rules.FS)
	if err != nil {
		errorf(stderr, "sep47idx sync: rules: %v\n", err)
		return exitError
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DB), 0o750); err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	st, err := store.Open(ctx, cfg.DB, store.Options{Passphrase: cfg.Network.Passphrase})
	if err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	defer func() { _ = st.Close() }() // close error after a completed seed carries nothing actionable

	if err := recordSyncState(ctx, st, cfg.RPCURL, ruleFiles); err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	reg := claims.Default(sepmeta.DefaultLimits())
	reg.AllowInvocation = *allowInvocation
	ix := &ingest.Indexer{Store: st, RPC: client, Rules: ruleFiles, Matcher: match.Matcher{PartialThreshold: *threshold},
		Limits: sepmeta.DefaultLimits(), Claims: reg, Log: log, Now: time.Now}

	f, err := os.Open(*seed) // #nosec G304 -- operator-supplied seed path
	if err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	defer func() { _ = f.Close() }() // read-only
	res, err := ix.Seed(ctx, ingest.SeedFileSource{R: f})
	if err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	if _, err := fmt.Fprintf(stdout, "seeded %d of %d contracts (%d not found, %d archived, %d hash hints differed); fetched %d wasm; last_ledger %d\n",
		res.Seeded, res.Requested, res.NotFound, res.Archived, res.HintMismatch, res.WasmFetched, res.LastLedger); err != nil {
		return exitError
	}
	return exitOK
}

// recordSyncState stores the RPC URL and loaded ruleset versions, which
// --recompute and /v1/health report.
func recordSyncState(ctx context.Context, st *store.Store, rpcURL string, rf []match.RuleFile) error {
	var versions []string
	for _, r := range rf {
		versions = append(versions, r.RulesetVersion)
	}
	if err := st.SetState(ctx, store.KeyRPCURL, rpcURL); err != nil {
		return err
	}
	return st.SetState(ctx, store.KeyRulesetVersions, strings.Join(versions, ","))
}
