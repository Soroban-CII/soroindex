package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
)

func init() {
	commands["query"] = command{summary: "query current contracts by SEP and trust tier", run: runQuery}
}

func runQuery(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := config.RegisterCommon(fs)
	all := fs.String("implements", "", "comma-separated SEPs required together (AND)")
	anySEPs := fs.String("implements-any", "", "comma-separated alternative SEPs (OR)")
	tier := fs.String("tier", "declared", "declared, inferred, verified or protocol")
	kind := fs.String("kind", "", "wasm, wasm_ref or sac")
	limit := fs.Int("limit", 50, "page size, 1..500")
	cursor := fs.String("cursor", "", "opaque continuation cursor")
	csvMode := fs.Bool("csv", false, "write CSV")
	if err := fs.Parse(args); err != nil {
		return readExit("query", err, stderr)
	}
	cfg, err := resolve()
	if err != nil {
		return readExit("query", err, stderr)
	}
	if fs.NArg() != 0 || *limit < 1 || *limit > 500 || (*csvMode && cfg.JSON) {
		return readExit("query", errors.New("unexpected argument, invalid limit or conflicting --json/--csv"), stderr)
	}
	seps, err := store.ParseSEPs(*all)
	if err != nil {
		return readExit("query", err, stderr)
	}
	alternatives, err := store.ParseSEPs(*anySEPs)
	if err != nil {
		return readExit("query", err, stderr)
	}
	err = readIndex(cfg, func(ctx context.Context, s *store.Store, rulesets map[int]string) error {
		page, err := s.Contracts(ctx, store.Filter{Implements: seps, ImplementsAny: alternatives, Tier: *tier, Kind: *kind, Limit: *limit, Cursor: *cursor, Rulesets: rulesets})
		if err != nil {
			return err
		}
		if cfg.JSON {
			return jsonOutput(stdout, page)
		}
		if *csvMode {
			w := csv.NewWriter(stdout)
			if err = w.Write([]string{"contract_id", "kind", "wasm_hash", "exec_ref_owner", "exec_ref_tag", "archived", "tier"}); err != nil {
				return err
			}
			for _, c := range page.Contracts {
				hash, owner, tag := "", "", ""
				if c.WasmHash != nil {
					hash = *c.WasmHash
				}
				if c.ExecRef != nil {
					owner = c.ExecRef.Owner
					tag = c.ExecRef.Tag
				}
				if err = w.Write([]string{c.ID, c.Kind, hash, owner, tag, strconv.FormatBool(c.Archived), page.Tier}); err != nil {
					return err
				}
			}
			w.Flush()
			if err = w.Error(); err != nil {
				return err
			}
		} else {
			if _, err = fmt.Fprintf(stdout, "Tier: %s; %d contracts on this page\n", page.Tier, len(page.Contracts)); err != nil {
				return err
			}
			for _, c := range page.Contracts {
				hash := "unresolved"
				if c.WasmHash != nil {
					hash = *c.WasmHash
				}
				if c.Kind == "sac" {
					hash = "protocol"
				}
				if _, err = fmt.Fprintf(stdout, "%s  %s  %s\n", c.ID, c.Kind, hash); err != nil {
					return err
				}
			}
		}
		if page.NextCursor != nil {
			_, err = fmt.Fprintf(stderr, "next cursor: %s\n", *page.NextCursor)
		}
		return err
	})
	return readExit("query", err, stderr)
}
