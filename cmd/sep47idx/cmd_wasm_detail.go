package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/store"
)

// signature renders a function's types as "(a, b) -> c", or "-> ()" when it
// returns nothing, so an author can compare it with their contract's spec.
func signature(s match.Signature) string {
	out := "()"
	if len(s.Outputs) > 0 {
		out = strings.Join(s.Outputs, ", ")
	}
	return "(" + strings.Join(s.Inputs, ", ") + ") -> " + out
}

// writeMatchEvidence lists the functions that kept a match from being
// complete. Without it, human output gives only a count, and an author has to
// rerun with --json to learn what to fix.
func writeMatchEvidence(w io.Writer, m store.WasmMatch) error {
	var missing []string
	var mismatched []match.Mismatch
	if err := json.Unmarshal(m.Missing, &missing); err != nil {
		return fmt.Errorf("stored missing functions for SEP-%d: %w", m.SEP, err)
	}
	if err := json.Unmarshal(m.Mismatched, &mismatched); err != nil {
		return fmt.Errorf("stored mismatched functions for SEP-%d: %w", m.SEP, err)
	}
	if len(missing) > 0 {
		if _, err := fmt.Fprintf(w, "  Missing: %s\n", strings.Join(missing, ", ")); err != nil {
			return err
		}
	}
	for _, mm := range mismatched {
		label := "Mismatched"
		if mm.Optional {
			label = "Mismatched optional"
		}
		var err error
		if mm.Reason == "duplicate" {
			_, err = fmt.Fprintf(w, "  %s %s: defined more than once\n", label, mm.Fn)
		} else {
			_, err = fmt.Fprintf(w, "  %s %s: expected %s, found %s\n", label, mm.Fn, signature(mm.Expected), signature(mm.Actual))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func init() {
	commands["wasm"] = command{summary: "show parsed Wasm metadata and interface evidence", run: runWasm}
}

func runWasm(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wasm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := config.RegisterCommon(fs)
	limit := fs.Int("limit", 50, "page size of live users, 1..500")
	cursor := fs.String("cursor", "", "opaque continuation cursor")
	hash, err := parseObject(fs, args)
	if err != nil {
		return readExit("wasm", err, stderr)
	}
	cfg, err := resolve()
	if err != nil {
		return readExit("wasm", err, stderr)
	}
	if err = store.ValidateWasmHash(hash); err != nil {
		return readExit("wasm", err, stderr)
	}
	if *limit < 1 || *limit > 500 {
		return readExit("wasm", store.ErrBadQuery, stderr)
	}
	err = readIndex(cfg, func(ctx context.Context, s *store.Store, rulesets map[int]string) error {
		d, err := s.Wasm(ctx, hash, *limit, *cursor, rulesets)
		if err != nil {
			return err
		}
		if cfg.JSON {
			return jsonOutput(stdout, d)
		}
		if _, err = fmt.Fprintf(stdout, "%s: %s (parser %s), as of ledger %d\nMetadata: %s\n", hash, d.ParseStatus, d.ParserVersion, d.AsOfLedger, d.Meta); err != nil {
			return err
		}
		for _, c := range d.Claims {
			if _, err = fmt.Fprintf(stdout, "Declared SEP-%d (%s), token=%q, anomaly=%v\n", c.SEP, c.Source, c.RawToken, c.Anomaly); err != nil {
				return err
			}
		}
		for _, a := range d.Anomalies {
			if _, err = fmt.Fprintf(stdout, "Anomaly %s: %q\n", a.Anomaly, a.RawToken); err != nil {
				return err
			}
		}
		for _, m := range d.Matches {
			if _, err = fmt.Fprintf(stdout, "Inferred SEP-%d: %s (%s), %d functions matched\n", m.SEP, m.Status, m.RulesetVersion, m.OKCount); err != nil {
				return err
			}
			if err = writeMatchEvidence(stdout, m); err != nil {
				return err
			}
		}
		if _, err = fmt.Fprintf(stdout, "Live contracts on this page: %d\n", len(d.Contracts)); err != nil {
			return err
		}
		for _, c := range d.Contracts {
			if _, err = fmt.Fprintln(stdout, c.ID); err != nil {
				return err
			}
		}
		if d.NextCursor != nil {
			_, err = fmt.Fprintf(stderr, "next cursor: %s\n", *d.NextCursor)
		}
		return err
	})
	return readExit("wasm", err, stderr)
}
