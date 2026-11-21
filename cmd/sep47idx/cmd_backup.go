package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"os"
	"time"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
)

func init() {
	commands["backup"] = command{
		summary: "create a consistent online snapshot of the database (safe while sync runs)",
		run:     runBackup,
	}
}

// runBackup implements `sep47idx backup --out <dst>`.
//
// It opens the source database (the same path sync uses) and runs SQLite's
// VACUUM INTO to create a point-in-time snapshot at dst. The snapshot is
// then opened read-only and validated: network passphrase, schema version
// and committed ledger must all be internally consistent. The command is
// safe to run while sep47idx sync is writing.
//
// dst must not already exist: SQLite refuses to overwrite an existing file.
func runBackup(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	resolve := config.RegisterCommon(flags)
	out := flags.String("out", "", "destination database path (must not already exist)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitError
	}
	cfg, err := resolve()
	if err != nil {
		errorf(stderr, "sep47idx backup: %v\n", err)
		return exitError
	}
	if *out == "" || flags.NArg() != 0 {
		errorf(stderr, "sep47idx backup: --out is required; no positional arguments are accepted\n")
		return exitError
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	st, err := store.Open(ctx, cfg.DB, store.Options{Passphrase: cfg.Network.Passphrase})
	if err != nil {
		errorf(stderr, "sep47idx backup: %v\n", err)
		return exitError
	}
	defer func() { _ = st.Close() }()

	start := time.Now()
	if err := st.Backup(ctx, *out); err != nil {
		errorf(stderr, "sep47idx backup: %v\n", err)
		return exitError
	}
	elapsed := time.Since(start)

	ver, _ := store.SchemaVersion()
	ledger, _ := st.State(ctx, store.KeyLastLedger)
	if _, err := fmt.Fprintf(stdout, "backup written to %s (schema_version=%d, last_ledger=%s, elapsed=%s)\n",
		*out, ver, ledger, elapsed.Round(time.Millisecond)); err != nil {
		return exitError
	}
	return exitOK
}
