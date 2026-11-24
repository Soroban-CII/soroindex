package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/rules"
)

func init() {
	commands["doctor"] = command{summary: "check the database, RPC, lag, disk and rules, and suggest fixes", run: runDoctor}
}

// Check statuses. A skip is not a failure: it means an earlier check made
// this one meaningless, and the detail says which.
const (
	doctorPass = "pass"
	doctorFail = "fail"
	doctorSkip = "skip"
)

// doctorCheck is one diagnostic. Fix is empty for a passing check.
type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// doctorReport is the whole command output. It is a struct, not a map, so
// the JSON field order is stable for scripts and diffs.
type doctorReport struct {
	OK      bool          `json:"ok"`
	Network string        `json:"network"`
	DB      string        `json:"db"`
	Checks  []doctorCheck `json:"checks"`
}

func (r *doctorReport) add(c doctorCheck) {
	if c.Status == doctorFail {
		r.OK = false
	}
	r.Checks = append(r.Checks, c)
}

// runDoctor checks the parts an operator would otherwise test by hand. It
// opens the database read-only and makes one read-only RPC call; nothing it
// does can change the index. Every check runs even after another fails, so
// one invocation shows all the problems.
func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := config.RegisterCommon(fs)
	maxLag := fs.Uint("max-lag", 0, "fail when the index is more than this many ledgers behind; 0 reports lag without failing")
	minFreeMB := fs.Uint64("min-free-mb", 100, "fail when the database filesystem has less free space than this")
	if err := fs.Parse(args); err != nil {
		return readExit("doctor", err, stderr)
	}
	cfg, err := resolve()
	if err != nil {
		return readExit("doctor", err, stderr)
	}
	if fs.NArg() != 0 {
		return readExit("doctor", fmt.Errorf("takes no positional arguments"), stderr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.RPCTimeout)
	defer cancel()

	report := doctorReport{OK: true, Network: cfg.Network.Name, DB: cfg.DB, Checks: []doctorCheck{}}
	var s *store.Store
	var lastLedger *uint32
	var latest *uint32

	// Rules need neither the database nor RPC, so a broken rule file is
	// reported even when everything else is down.
	if _, err := match.LoadRules(rules.FS); err != nil {
		report.add(doctorCheck{Name: "rules", Status: doctorFail, Detail: err.Error(),
			Fix: "the binary's embedded rules are damaged; reinstall the released binary and report this"})
	} else {
		report.add(doctorCheck{Name: "rules", Status: doctorPass, Detail: "embedded rule files load and validate"})
	}

	s, err = store.Open(ctx, cfg.DB, store.Options{Passphrase: cfg.Network.Passphrase, ReadOnly: true})
	if err != nil {
		report.add(doctorCheck{Name: "database", Status: doctorFail, Detail: err.Error(), Fix: databaseFix(err)})
	} else {
		defer func() { _ = s.Close() }()
		report.add(doctorCheck{Name: "database", Status: doctorPass,
			Detail: fmt.Sprintf("%s opens read-only and records the %s network passphrase", cfg.DB, cfg.Network.Name)})

		var check string
		if err := s.DB.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&check); err != nil || check != "ok" {
			detail := check
			if err != nil {
				detail = err.Error()
			}
			report.add(doctorCheck{Name: "integrity", Status: doctorFail, Detail: "quick_check: " + detail,
				Fix: "restore the database from a backup, or re-run sync from a seed file"})
		} else {
			report.add(doctorCheck{Name: "integrity", Status: doctorPass, Detail: "PRAGMA quick_check reports ok"})
		}

		if v, err := s.State(ctx, store.KeyLastLedger); err != nil {
			report.add(doctorCheck{Name: "sync_state", Status: doctorFail, Detail: err.Error(),
				Fix: "run: sep47idx sync --network " + cfg.Network.Name + " --seed <contracts.csv> (or --start-ledger <n>)"})
		} else if n, err := parseLedger(v); err != nil {
			report.add(doctorCheck{Name: "sync_state", Status: doctorFail, Detail: "last_ledger is not a ledger number: " + v,
				Fix: "restore sync_state from a backup, or re-seed the index"})
		} else {
			lastLedger = &n
			report.add(doctorCheck{Name: "sync_state", Status: doctorPass, Detail: fmt.Sprintf("last_ledger is %d", n)})
		}
	}

	if err := cfg.RequireRPC(); err != nil {
		report.add(doctorCheck{Name: "rpc", Status: doctorFail, Detail: err.Error(),
			Fix: "pass --rpc-url or set " + config.EnvPrefix + "RPC_URL"})
	} else if client, err := rpc.New(rpc.Config{URL: cfg.RPCURL, Timeout: cfg.RPCTimeout, Concurrency: cfg.Concurrency}); err != nil {
		report.add(doctorCheck{Name: "rpc", Status: doctorFail, Detail: err.Error(), Fix: "check --rpc-url"})
	} else if network, err := client.GetNetwork(ctx); err != nil {
		report.add(doctorCheck{Name: "rpc", Status: doctorFail, Detail: cfg.RPCURL + ": " + err.Error(),
			Fix: "check the URL, DNS and firewall; the RPC must be reachable from here"})
	} else if network.Passphrase != cfg.Network.Passphrase {
		report.add(doctorCheck{Name: "rpc", Status: doctorFail,
			Detail: fmt.Sprintf("%s serves %q, not %s", cfg.RPCURL, network.Passphrase, cfg.Network.Name),
			Fix:    "point --rpc-url at an RPC for " + cfg.Network.Name})
	} else if tip, err := client.GetLatestLedger(ctx); err != nil {
		report.add(doctorCheck{Name: "rpc", Status: doctorFail, Detail: err.Error(),
			Fix: "retry; if it persists, the RPC is unhealthy, use another endpoint"})
	} else {
		latest = &tip.Sequence
		report.add(doctorCheck{Name: "rpc", Status: doctorPass,
			Detail: fmt.Sprintf("%s serves %s at ledger %d", cfg.RPCURL, cfg.Network.Name, tip.Sequence)})
	}

	switch {
	case latest == nil:
		report.add(doctorCheck{Name: "lag", Status: doctorSkip, Detail: "the RPC tip was not read"})
	case lastLedger == nil:
		report.add(doctorCheck{Name: "lag", Status: doctorSkip, Detail: "sync_state has no last_ledger"})
	case *latest < *lastLedger:
		report.add(doctorCheck{Name: "lag", Status: doctorFail,
			Detail: fmt.Sprintf("indexed ledger %d is ahead of the RPC's latest ledger %d", *lastLedger, *latest),
			Fix:    "the database may belong to a reset network, or the RPC is behind; check both before syncing"})
	default:
		lag := *latest - *lastLedger
		if *maxLag > 0 && uint64(lag) > uint64(*maxLag) {
			report.add(doctorCheck{Name: "lag", Status: doctorFail, Detail: fmt.Sprintf("%d ledgers behind", lag),
				Fix: "check that the follow process is running and advancing, not only that health is green"})
		} else {
			report.add(doctorCheck{Name: "lag", Status: doctorPass, Detail: fmt.Sprintf("%d ledgers behind the RPC tip", lag)})
		}
	}

	dir := filepath.Dir(cfg.DB)
	free, err := freeBytes(dir)
	switch {
	case err != nil:
		report.add(doctorCheck{Name: "disk", Status: doctorFail, Detail: dir + ": " + err.Error(),
			Fix: "check that the database directory exists and is readable"})
	case free < *minFreeMB<<20:
		report.add(doctorCheck{Name: "disk", Status: doctorFail,
			Detail: fmt.Sprintf("%s has %d MiB free, below the %d MiB requirement", dir, free>>20, *minFreeMB),
			Fix:    "free space, move the database to a larger volume, or lower --min-free-mb deliberately"})
	default:
		report.add(doctorCheck{Name: "disk", Status: doctorPass, Detail: fmt.Sprintf("%s has %d MiB free", dir, free>>20)})
	}

	if cfg.JSON {
		if err := jsonOutput(stdout, report); err != nil {
			return readExit("doctor", err, stderr)
		}
	} else if err := writeDoctor(stdout, report); err != nil {
		return readExit("doctor", err, stderr)
	}
	if !report.OK {
		return exitError
	}
	return exitOK
}

// databaseFix turns the store's opening error into the operator's next step.
// The messages are sentinels from internal/store, so this is not string
// matching on arbitrary text.
func databaseFix(err error) string {
	switch {
	case errors.Is(err, store.ErrSchemaTooNew):
		return "use a binary at least as new as the database, or restore a backup"
	case errors.Is(err, store.ErrSchemaTooOld):
		return "run: sep47idx sync --network <network> (it applies migrations)"
	case errors.Is(err, store.ErrReadOnlyUninitialized):
		return "run: sep47idx sync --network <network> --seed <contracts.csv>"
	case errors.Is(err, store.ErrNetworkMismatch):
		return "pass the matching --network, or point --db at that network's database"
	}
	return "check --db; if the file is missing, run: sep47idx sync --network <network> --seed <contracts.csv>"
}

func writeDoctor(w io.Writer, r doctorReport) error {
	for _, c := range r.Checks {
		if _, err := fmt.Fprintf(w, "%-4s %-10s %s\n", strings.ToUpper(c.Status), c.Name, c.Detail); err != nil {
			return err
		}
		if c.Fix != "" {
			if _, err := fmt.Fprintf(w, "     fix: %s\n", c.Fix); err != nil {
				return err
			}
		}
	}
	if r.OK {
		_, err := fmt.Fprintf(w, "\nAll checks passed for %s (%s).\n", r.Network, r.DB)
		return err
	}
	_, err := fmt.Fprintf(w, "\nSome checks failed for %s (%s). Fix them in the order shown.\n", r.Network, r.DB)
	return err
}

func parseLedger(v string) (uint32, error) {
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(n), nil
}
