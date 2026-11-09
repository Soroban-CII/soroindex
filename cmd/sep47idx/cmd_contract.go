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
	commands["contract"] = command{summary: "show a contract's claims and optional full history", run: runContract}
}

func runContract(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("contract", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := config.RegisterCommon(fs)
	history := fs.Bool("history", false, "include claims for every code version")
	id, err := parseObject(fs, args)
	if err != nil {
		return readExit("contract", err, stderr)
	}
	cfg, err := resolve()
	if err != nil {
		return readExit("contract", err, stderr)
	}
	if err = store.ValidateContractID(id); err != nil {
		return readExit("contract", err, stderr)
	}
	err = readIndex(cfg, func(ctx context.Context, s *store.Store, rulesets map[int]string) error {
		if *history {
			d, err := s.History(ctx, id, cfg.Network.Name, rulesets)
			if err != nil {
				return err
			}
			if cfg.JSON {
				return jsonOutput(stdout, d)
			}
			if _, err = fmt.Fprintf(stdout, "%s (%s), as of ledger %d\n", id, d.Network, d.AsOfLedger); err != nil {
				return err
			}
			for _, v := range d.History {
				hash := "no Wasm"
				if v.WasmHash != nil {
					hash = *v.WasmHash
				}
				to := "current"
				if v.ToLedger != nil {
					to = fmt.Sprint(*v.ToLedger)
				}
				if _, err = fmt.Fprintf(stdout, "%d..%s  %s\n", v.FromLedger, to, hash); err != nil {
					return err
				}
				isUnresolved := v.ExecRef != nil && v.WasmHash == nil
				if err = printClaims(stdout, v.Claims, isUnresolved, false); err != nil {
					return err
				}
			}
			return nil
		}
		d, err := s.Contract(ctx, id, cfg.Network.Name, rulesets)
		if err != nil {
			return err
		}
		if cfg.JSON {
			return jsonOutput(stdout, d)
		}
		hash := "no Wasm"
		if d.WasmHash != nil {
			hash = *d.WasmHash
		}
		if _, err = fmt.Fprintf(stdout, "%s (%s, %s), archived=%t, as of ledger %d\nWasm: %s\n", id, d.Network, d.Kind, d.Archived, d.AsOfLedger, hash); err != nil {
			return err
		}
		isUnresolved := d.Kind == "wasm_ref" && d.WasmHash == nil
		if d.ExecRef != nil {
			refStatus := ""
			if isUnresolved {
				if d.Archived {
					refStatus = " (archived reference)"
				} else {
					refStatus = " (unresolved reference)"
				}
			}
			if _, err = fmt.Fprintf(stdout, "Executable reference: %s / %s%s\n", d.ExecRef.Owner, d.ExecRef.Tag, refStatus); err != nil {
				return err
			}
		}
		return printClaims(stdout, d.Claims, isUnresolved, d.Archived)
	})
	return readExit("contract", err, stderr)
}

func printClaims(out io.Writer, claims []store.ClaimDetail, isUnresolvedRef, isArchivedRef bool) error {
	if len(claims) == 0 {
		if isUnresolvedRef {
			if isArchivedRef {
				_, err := fmt.Fprintln(out, "Unresolved executable reference: reference entry is archived.")
				return err
			}
			_, err := fmt.Fprintln(out, "Unresolved executable reference: reference entry not found.")
			return err
		}
		_, err := fmt.Fprintln(out, "No stored SEP evidence.")
		return err
	}
	for _, c := range claims {
		inferred, verified := "no match row", "not run"
		if c.Inferred != nil {
			inferred = c.Inferred.Status + " (" + c.Inferred.RulesetVersion + ")"
		}
		if c.Verified != nil {
			verified = fmt.Sprintf("%s (%s %s; stale=%t)", c.Verified.Verdict, c.Verified.Tool, c.Verified.ToolVersion, c.Verified.Stale)
		}
		if _, err := fmt.Fprintf(out, "SEP-%d: declared=%t, inferred=%s, verified=%s, protocol=%t\n", c.SEP, c.Declared, inferred, verified, c.Protocol); err != nil {
			return err
		}
		if c.Inferred != nil {
			if c.Inferred.Status == "no_spec" {
				if _, err := fmt.Fprintln(out, "  No spec: Wasm has no contractspecv0 section"); err != nil {
					return err
				}
			} else {
				if err := writeEvidence(out, c.SEP, c.Inferred.Missing, c.Inferred.Mismatched); err != nil {
					return err
				}
			}
		}
		if c.Verified != nil {
			if _, err := fmt.Fprintln(out, "A pass means the suite's checks passed; it is not a security audit."); err != nil {
				return err
			}
		}
	}
	return nil
}
