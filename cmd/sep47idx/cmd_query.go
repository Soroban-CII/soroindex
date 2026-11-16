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
	allPages := fs.Bool("all", false, "export all remaining pages (requires --csv)")
	if err := fs.Parse(args); err != nil {
		return readExit("query", err, stderr)
	}
	cfg, err := resolve()
	if err != nil {
		return readExit("query", err, stderr)
	}
	if fs.NArg() != 0 || *limit < 1 || *limit > 500 || (*csvMode && cfg.JSON) || (*allPages && !*csvMode) {
		return readExit("query", errors.New("unexpected argument, invalid limit or conflicting --json/--csv or --all without --csv"), stderr)
	}
	seps, err := store.ParseSEPs(*all)
	if err != nil {
		return readExit("query", err, stderr)
	}
	alternatives, err := store.ParseSEPs(*anySEPs)
	if err != nil {
		return readExit("query", err, stderr)
	}
	matched := 0
	err = readIndex(cfg, func(ctx context.Context, s *store.Store, rulesets map[int]string) error {
		filter := store.Filter{Implements: seps, ImplementsAny: alternatives, Tier: *tier, Kind: *kind, Limit: *limit, Cursor: *cursor, Rulesets: rulesets}
		page, err := s.Contracts(ctx, filter)
		if err != nil {
			return err
		}
		if cfg.JSON {
			matched = len(page.Contracts)
			return jsonOutput(stdout, page)
		}
		if *csvMode {
			count, err := exportQueryCSV(ctx, stdout, page, filter, *allPages, s.Contracts)
			matched = count
			if err != nil {
				return err
			}
			if *allPages {
				return nil
			}
		} else {
			matched = len(page.Contracts)
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
	code := readExit("query", err, stderr)
	if code == exitOK && matched == 0 {
		errorf(stderr, "sep47idx query: no contract matches the filter\n")
		return exitNotFound
	}
	return code
}

// exportQueryCSV flushes each bounded page before fetching its successor.
// Each query observes its own database state; this is not a cross-page snapshot.
// It reports how many contracts were written, so the caller can tell an empty
// result from a full one.
func exportQueryCSV(ctx context.Context, out io.Writer, page store.Page, filter store.Filter, all bool, query func(context.Context, store.Filter) (store.Page, error)) (int, error) {
	w := csv.NewWriter(out)
	if err := w.Write([]string{"contract_id", "kind", "wasm_hash", "exec_ref_owner", "exec_ref_tag", "archived", "tier"}); err != nil {
		return 0, err
	}
	written := 0
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		for _, c := range page.Contracts {
			hash, owner, tag := "", "", ""
			if c.WasmHash != nil {
				hash = *c.WasmHash
			}
			if c.ExecRef != nil {
				owner, tag = c.ExecRef.Owner, c.ExecRef.Tag
			}
			if err := w.Write([]string{c.ID, c.Kind, hash, owner, tag, strconv.FormatBool(c.Archived), page.Tier}); err != nil {
				return written, err
			}
			written++
		}
		w.Flush()
		if err := w.Error(); err != nil {
			return written, err
		}
		if !all || page.NextCursor == nil {
			return written, nil
		}
		filter.Cursor = *page.NextCursor
		if err := ctx.Err(); err != nil {
			return written, fmt.Errorf("CSV export before cursor %q: %w", filter.Cursor, err)
		}
		var err error
		page, err = query(ctx, filter)
		if err != nil {
			return written, fmt.Errorf("CSV export before cursor %q: %w", filter.Cursor, err)
		}
	}
}
