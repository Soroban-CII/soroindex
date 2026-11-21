package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
)

func init() {
	commands["migrate"] = command{
		summary: "show migration status or preview pending migrations without applying them",
		run:     runMigrate,
	}
}

// runMigrate implements
// `sep47idx migrate --status` and `sep47idx migrate --dry-run`.
// Neither subcommand makes any change to the database.
func runMigrate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := config.RegisterCommon(fs)
	status := fs.Bool("status", false, "list all migrations with applied/pending state and checksums")
	dryRun := fs.Bool("dry-run", false, "list pending migrations that would be applied, without changing the database")
	if err := fs.Parse(args); err != nil {
		return readExit("migrate", err, stderr)
	}
	cfg, err := resolve()
	if err != nil {
		return readExit("migrate", err, stderr)
	}
	if fs.NArg() != 0 {
		return readExit("migrate", errors.New("takes no positional arguments"), stderr)
	}
	if !*status && !*dryRun {
		return readExit("migrate", errors.New("one of --status or --dry-run is required"), stderr)
	}
	if *status && *dryRun {
		return readExit("migrate", errors.New("--status and --dry-run are mutually exclusive"), stderr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.RPCTimeout)
	defer cancel()

	o := store.MigrateStatusOptions{Passphrase: cfg.Network.Passphrase}
	var rows []store.MigrationRow
	if *status {
		rows, err = store.MigrateStatus(ctx, cfg.DB, o)
	} else {
		rows, err = store.MigrateDryRun(ctx, cfg.DB, o)
	}
	if err != nil {
		return readExit("migrate", err, stderr)
	}

	if cfg.JSON {
		return readExit("migrate", jsonOutput(stdout, rows), stderr)
	}
	if *dryRun {
		if len(rows) == 0 {
			if _, e := fmt.Fprintln(stdout, "no pending migrations"); e != nil {
				return exitError
			}
			return exitOK
		}
		if _, e := fmt.Fprintf(stdout, "%d pending migration(s):\n", len(rows)); e != nil {
			return exitError
		}
	}
	for _, r := range rows {
		state := "pending"
		if r.Applied {
			state = "applied"
		}
		tamper := ""
		if r.Tampered {
			tamper = " [TAMPERED]"
		}
		if _, e := fmt.Fprintf(stdout, "  v%04d  %-40s  %s  %s%s\n", r.Version, r.Name, r.Checksum[:16]+"...", state, tamper); e != nil {
			return exitError
		}
	}
	return exitOK
}
