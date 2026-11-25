package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

func init() {
	commands["seed"] = command{
		summary: "validate a seed CSV against the RPC without writing to the database",
		run:     runSeedCheck,
	}
}

// runSeedCheck implements sep47idx seed --check <file>: a dry-run of the seed
// that reads instance entries from the network and reports totals without
// touching the database. Its Found count matches what sync --seed would store.
func runSeedCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := config.RegisterCommon(fs)
	check := fs.String("check", "", "CSV file to validate against the RPC (required)")
	if err := fs.Parse(args); err != nil {
		return readExit("seed", err, stderr)
	}
	cfg, err := resolve()
	if err != nil {
		return readExit("seed", err, stderr)
	}
	if *check == "" {
		return readExit("seed", fmt.Errorf("--check <file> is required"), stderr)
	}
	if fs.NArg() != 0 {
		return readExit("seed", fmt.Errorf("positional arguments are not accepted"), stderr)
	}
	if err := cfg.RequireRPC(); err != nil {
		return readExit("seed", err, stderr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := rpc.New(rpc.Config{URL: cfg.RPCURL, Timeout: cfg.RPCTimeout, Concurrency: cfg.Concurrency})
	if err != nil {
		return readExit("seed", err, stderr)
	}
	network, err := client.GetNetwork(ctx)
	if err != nil {
		return readExit("seed", fmt.Errorf("getNetwork: %w", err), stderr)
	}
	if network.Passphrase != cfg.Network.Passphrase {
		return readExit("seed", fmt.Errorf("RPC network does not match %s", cfg.Network.Name), stderr)
	}

	f, err := os.Open(*check) // #nosec G304 -- operator-supplied seed path
	if err != nil {
		return readExit("seed", err, stderr)
	}
	defer func() { _ = f.Close() }()

	ix := &ingest.Indexer{RPC: client, Limits: sepmeta.DefaultLimits()}
	res, err := ix.CheckSeed(ctx, f)
	if err != nil {
		return readExit("seed", err, stderr)
	}

	if cfg.JSON {
		if err := jsonOutput(stdout, res); err != nil {
			return readExit("seed", err, stderr)
		}
	} else if err := printSeedCheck(stdout, res); err != nil {
		return readExit("seed", err, stderr)
	}

	if len(res.Invalid) > 0 {
		return exitError
	}
	return exitOK
}

func printSeedCheck(w io.Writer, res ingest.SeedCheckResult) error {
	if _, err := fmt.Fprintf(w, "rows: %d unique (%d duplicate", res.Rows, res.Duplicates); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "); latest_ledger: %d\n", res.LatestLedger); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "found: %d  not_found: %d  archived: %d", res.Found, res.NotFound, res.Archived); err != nil {
		return err
	}
	if res.HintMismatch > 0 {
		if _, err := fmt.Fprintf(w, "  hint_mismatch: %d", res.HintMismatch); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if res.Found > 0 {
		if _, err := fmt.Fprintf(w, "kinds: wasm: %d  wasm_ref: %d  sac: %d\n", res.Wasm, res.WasmRef, res.SAC); err != nil {
			return err
		}
	}
	for _, inv := range res.Invalid {
		if _, err := fmt.Fprintln(w, "invalid:", inv); err != nil {
			return err
		}
	}
	return nil
}
