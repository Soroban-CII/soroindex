package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Soroban-CII/soroindex/internal/clierr"
	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
)

func init() {
	commands["check"] = command{
		summary: "compare a seeded sample of stored contracts with live RPC entries",
		run:     runCheck,
	}
}

// runCheck implements the data-quality check: it re-reads a reproducible
// sample of contracts from RPC and reports fields that disagree with the
// index. It never writes, so it is safe against a production database.
func runCheck(args []string, stdout, stderr io.Writer) int {
	jsonMode := jsonRequested(args)
	fs := newFlagSet("check", stderr, jsonMode)
	resolve := config.RegisterCommon(fs)
	sample := fs.Int("sample", 100, "number of contracts to compare (1..100000)")
	seed := fs.Int64("seed", 1, "sampling seed; the same seed and database pick the same contracts")
	if err := parse(fs, args); err != nil {
		return readExit("check", err, stderr, jsonMode)
	}
	cfg, err := resolve()
	if err != nil {
		return readExit("check", err, stderr, jsonMode)
	}
	if fs.NArg() != 0 || *sample < 1 || *sample > 100000 {
		return readExit("check", clierr.Newf("bad_request", "sample must be 1..100000 and positional arguments are not accepted"), stderr, jsonMode)
	}
	if err = cfg.RequireRPC(); err != nil {
		return readExit("check", err, stderr, jsonMode)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := store.Open(ctx, cfg.DB, store.Options{Passphrase: cfg.Network.Passphrase, ReadOnly: true})
	if err != nil {
		return readExit("check", err, stderr, jsonMode)
	}
	defer func() { _ = s.Close() }()
	client, err := rpc.New(rpc.Config{URL: cfg.RPCURL, Timeout: cfg.RPCTimeout, Concurrency: cfg.Concurrency})
	if err != nil {
		return readExit("check", err, stderr, jsonMode)
	}
	network, err := client.GetNetwork(ctx)
	if err != nil {
		return readExit("check", err, stderr, jsonMode)
	}
	if network.Passphrase != cfg.Network.Passphrase {
		return readExit("check", clierr.Newf("network_mismatch", "RPC network does not match %s", cfg.Network.Name), stderr, jsonMode)
	}
	res, err := (&ingest.Checker{Store: s, RPC: client}).Check(ctx, ingest.CheckOptions{Sample: *sample, Seed: *seed})
	if err != nil {
		return readExit("check", err, stderr, jsonMode)
	}
	if cfg.JSON {
		if err := jsonOutput(stdout, res); err != nil {
			return readExit("check", err, stderr, jsonMode)
		}
	} else if err := printCheck(stdout, cfg.Network.Name, res); err != nil {
		return readExit("check", err, stderr, jsonMode)
	}
	if len(res.Differences) > 0 {
		return exitError
	}
	return exitOK
}

func printCheck(w io.Writer, network string, res ingest.CheckResult) error {
	if _, err := fmt.Fprintf(w, "%s: checked %d of %d sampled contracts at last_ledger %d against RPC latest %d\n",
		network, res.Checked, res.Sampled, res.LastLedger, res.LatestLedger); err != nil {
		return err
	}
	if res.ChangedAfterIndex > 0 || res.Archived > 0 {
		if _, err := fmt.Fprintf(w, "skipped %d changed after last_ledger and %d archived\n", res.ChangedAfterIndex, res.Archived); err != nil {
			return err
		}
	}
	if len(res.Differences) == 0 {
		_, err := fmt.Fprintln(w, "no differences")
		return err
	}
	if _, err := fmt.Fprintf(w, "%d difference(s):\n", len(res.Differences)); err != nil {
		return err
	}
	for _, d := range res.Differences {
		if _, err := fmt.Fprintf(w, "  %s %s: stored %q, rpc %q (rpc ledger %d)\n", d.ContractID, d.Field, d.Stored, d.RPC, d.RPCLedger); err != nil {
			return err
		}
	}
	return nil
}
