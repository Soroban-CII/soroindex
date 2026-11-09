package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
)

// statsOutput is the stats JSON: the stored snapshot, plus the RPC tip and
// lag when --lag asked for them. The two fields are pointers so a snapshot
// read without --lag says nothing about lag, rather than reporting 0.
type statsOutput struct {
	store.Statistics
	LatestLedger *uint32 `json:"latest_ledger,omitempty"`
	Lag          *uint32 `json:"lag,omitempty"`
}

// rpcTip reads the provider's latest ledger after checking that the provider
// serves the configured network, so lag never compares ledgers of two networks.
func rpcTip(ctx context.Context, cfg config.Common) (uint32, error) {
	if err := cfg.RequireRPC(); err != nil {
		return 0, err
	}
	client, err := rpc.New(rpc.Config{URL: cfg.RPCURL, Timeout: cfg.RPCTimeout, Concurrency: cfg.Concurrency})
	if err != nil {
		return 0, err
	}
	network, err := client.GetNetwork(ctx)
	if err != nil {
		return 0, fmt.Errorf("lag: %w", err)
	}
	if network.Passphrase != cfg.Network.Passphrase {
		return 0, fmt.Errorf("lag: RPC network does not match %s", cfg.Network.Name)
	}
	latest, err := client.GetLatestLedger(ctx)
	if err != nil {
		return 0, fmt.Errorf("lag: %w", err)
	}
	return latest.Sequence, nil
}

func init() {
	commands["stats"] = command{summary: "show stored adoption gap anomalies and sync progress", run: runStats}
}

func runStats(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := config.RegisterCommon(fs)
	withLag := fs.Bool("lag", false, "also read the RPC's latest ledger and report sync lag (network call)")
	if err := fs.Parse(args); err != nil {
		return readExit("stats", err, stderr)
	}
	cfg, err := resolve()
	if err != nil {
		return readExit("stats", err, stderr)
	}
	if fs.NArg() != 0 {
		return readExit("stats", fmt.Errorf("takes no positional arguments"), stderr)
	}
	err = readIndex(cfg, func(ctx context.Context, s *store.Store, rulesets map[int]string) error {
		d, err := s.Stats(ctx, cfg.Network.Name, rulesets)
		if err != nil {
			return err
		}
		out := statsOutput{Statistics: d}
		if *withLag {
			latest, err := rpcTip(ctx, cfg)
			if err != nil {
				return err
			}
			if latest < d.LastLedger {
				return fmt.Errorf("lag: stored ledger %d is ahead of RPC latest %d", d.LastLedger, latest)
			}
			lag := latest - d.LastLedger
			out.LatestLedger, out.Lag = &latest, &lag
		}
		if cfg.JSON {
			return jsonOutput(stdout, out)
		}
		t := d.Adoption
		_, err = fmt.Fprintf(stdout, "%s: last indexed ledger %d; %d stored contracts, %d archived\nLive: %d Wasm, %d Wasm references (%d unresolved), %d SAC (protocol)\nDeclared: %d/%d measured contracts, %d/%d used hashes\nUndeclared SEP-41 gap (inferred, %s): %d contracts, %d hashes\nAnomalies: %v\n", d.Network, d.LastLedger, d.TotalContracts, d.ArchivedContracts, t.LiveWasm, t.LiveWasmRef, t.WasmRefUnresolved, t.LiveSAC, t.DeclaringContracts, t.MeasuredContracts, t.DeclaringUsedHashes, t.UsedHashes, rulesets[41], t.GapContracts, t.GapUsedHashes, d.Anomalies)
		if err == nil && out.Lag != nil {
			_, err = fmt.Fprintf(stdout, "Sync: RPC latest ledger %d, lag %d ledgers\n", *out.LatestLedger, *out.Lag)
		}
		return err
	})
	return readExit("stats", err, stderr)
}
