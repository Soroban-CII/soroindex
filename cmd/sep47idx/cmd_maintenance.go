package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
)

func init() {
	commands["maintenance"] = command{
		summary: "check integrity, refresh query statistics and vacuum while no sync is writing",
		run:     runMaintenance,
	}
}

// runMaintenance implements
// `sep47idx maintenance [--enable-incremental-vacuum] [--timeout 1h] [--json]`.
// It exits 1 when another process holds the write lock or the integrity
// check finds problems, and prints the report in both cases.
func runMaintenance(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("maintenance", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := config.RegisterCommon(fs)
	enable := fs.Bool("enable-incremental-vacuum", false, "switch auto_vacuum to INCREMENTAL with a one-time full VACUUM (needs free disk about the database size)")
	timeout := fs.Duration("timeout", time.Hour, "give up after this long; nothing is left half-done (env SEP47IDX_TIMEOUT)")
	if err := fs.Parse(args); err != nil {
		return readExit("maintenance", err, stderr)
	}
	cfg, err := resolve()
	if err != nil {
		return readExit("maintenance", err, stderr)
	}
	if fs.NArg() != 0 {
		return readExit("maintenance", errors.New("takes no positional arguments"), stderr)
	}
	if *timeout <= 0 {
		return readExit("maintenance", errors.New("--timeout must be positive"), stderr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	r, err := store.Maintain(ctx, cfg.DB, store.MaintenanceOptions{Passphrase: cfg.Network.Passphrase, EnableIncrementalVacuum: *enable})
	// A report exists whenever a step ran: print it even on failure, so an
	// integrity problem is shown, not only counted.
	if len(r.Steps) > 0 {
		var werr error
		if cfg.JSON {
			werr = jsonOutput(stdout, r)
		} else {
			werr = printMaintenance(stdout, r)
		}
		if werr != nil && err == nil {
			err = werr
		}
	}
	return readExit("maintenance", err, stderr)
}

func printMaintenance(w io.Writer, r store.MaintenanceReport) error {
	size := func(d store.DatabaseSize) string {
		return fmt.Sprintf("%d bytes (%d pages of %d, %d free); on disk %d bytes", d.Bytes, d.PageCount, d.PageSize, d.FreelistCount, d.FileBytes)
	}
	lines := []string{fmt.Sprintf("database: %s", r.Database)}
	for _, s := range r.Steps {
		lines = append(lines, fmt.Sprintf("%-26s %s (%d ms)", s.Name+":", s.Result, s.DurationMS))
	}
	if !r.IntegrityOK {
		for _, p := range r.Integrity {
			lines = append(lines, "  integrity: "+p)
		}
	}
	lines = append(lines, "size before:               "+size(r.Before))
	if r.After.PageCount > 0 {
		lines = append(lines, "size after:                "+size(r.After))
	} else {
		lines = append(lines, "size after:                unchanged (stopped before any change)")
	}
	lines = append(lines, fmt.Sprintf("duration:                  %d ms", r.DurationMS))
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}
