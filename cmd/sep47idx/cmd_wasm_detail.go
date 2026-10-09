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
