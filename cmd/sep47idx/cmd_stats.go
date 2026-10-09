package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
)

func init() {
	commands["stats"] = command{summary: "show stored adoption gap anomalies and sync progress", run: runStats}
}

func runStats(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := config.RegisterCommon(fs)
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
		if cfg.JSON {
			return jsonOutput(stdout, d)
		}
		t := d.Adoption
		_, err = fmt.Fprintf(stdout, "%s: last indexed ledger %d; %d stored contracts, %d archived\nLive: %d Wasm, %d Wasm references (%d unresolved), %d SAC (protocol)\nDeclared: %d/%d measured contracts, %d/%d used hashes\nUndeclared SEP-41 gap (inferred, %s): %d contracts, %d hashes\nAnomalies: %v\n", d.Network, d.LastLedger, d.TotalContracts, d.ArchivedContracts, t.LiveWasm, t.LiveWasmRef, t.WasmRefUnresolved, t.LiveSAC, t.DeclaringContracts, t.MeasuredContracts, t.DeclaringUsedHashes, t.UsedHashes, rulesets[41], t.GapContracts, t.GapUsedHashes, d.Anomalies)
		return err
	})
	return readExit("stats", err, stderr)
}
