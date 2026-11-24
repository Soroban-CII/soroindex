package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Soroban-CII/soroindex/internal/clierr"
	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/outreach"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
)

func init() {
	commands["gap"] = command{
		summary: "list contracts that match a SEP's interface but declare nothing (inferred)",
		run:     runGap,
	}
}

// gapOutput is the --json shape. tier is always "inferred": these contracts
// make no claim (CLAUDE.md §5.6).
type gapOutput struct {
	SEP            int            `json:"sep"`
	Tier           string         `json:"tier"`
	RulesetVersion string         `json:"ruleset_version"`
	ParserVersion  string         `json:"parser_version"`
	Count          int            `json:"count"`
	Contracts      []store.GapRow `json:"contracts"`
	NextCursor     *string        `json:"next_cursor,omitempty"`
}

// outreachOutput is the --outreach --json shape. Like gapOutput, it says
// "inferred": none of these contracts declares a SEP.
type outreachOutput struct {
	SEP            int              `json:"sep"`
	Tier           string           `json:"tier"`
	RulesetVersion string           `json:"ruleset_version"`
	ParserVersion  string           `json:"parser_version"`
	Groups         []outreach.Group `json:"groups"`
	TotalGroups    int              `json:"total_groups"`
	TotalContracts int              `json:"total_contracts"`
}

// runGap implements `sep47idx gap --sep 41 [--limit N] [--cursor C] [--json]`
// and the grouped `--outreach` form.
func runGap(args []string, stdout, stderr io.Writer) int {
	jsonMode := jsonRequested(args)
	flags := newFlagSet("gap", stderr, jsonMode)
	resolve := config.RegisterCommon(flags)
	sep := flags.Int("sep", 41, "SEP number")
	limit := flags.Int("limit", 50, "page size, 1..500 (with --outreach: maximum Wasm hashes)")
	cursor := flags.String("cursor", "", "opaque continuation cursor")
	outreachMode := flags.Bool("outreach", false, "group the gap by Wasm hash with a contractmeta! line and pull-request body")
	if err := parse(flags, args); err != nil {
		return readExit("gap", err, stderr, jsonMode)
	}
	cfg, err := resolve()
	if err != nil {
		return fail(stderr, jsonMode, "gap", err)
	}
	if flags.NArg() != 0 || *sep < 1 || *sep > 999999 || *limit < 1 || *limit > 500 {
		return fail(stderr, jsonMode, "gap", clierr.Newf("bad_request", "unexpected argument, invalid SEP or limit (want 1..500)"))
	}
	if *outreachMode && *cursor != "" {
		return fail(stderr, jsonMode, "gap", clierr.Newf("bad_request", "--outreach groups the whole gap and does not page; --limit caps the number of Wasm hashes"))
	}
	ruleFiles, err := match.LoadRules(rules.FS)
	if err != nil {
		return fail(stderr, jsonMode, "gap", fmt.Errorf("rules: %w", err))
	}
	ruleset := ""
	for _, r := range ruleFiles {
		if r.SEP == *sep {
			ruleset = r.RulesetVersion
		}
	}
	if ruleset == "" {
		return fail(stderr, jsonMode, "gap", clierr.Newf("not_found", "no rule file for SEP-%d, so no interface can be inferred", *sep))
	}
	if _, err := os.Stat(cfg.DB); err != nil {
		return fail(stderr, jsonMode, "gap", clierr.Newf("bad_request", "%v (run sync first)", err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.RPCTimeout)
	defer cancel()
	st, err := store.Open(ctx, cfg.DB, store.Options{Passphrase: cfg.Network.Passphrase, ReadOnly: true})
	if err != nil {
		return fail(stderr, jsonMode, "gap", err)
	}
	defer func() { _ = st.Close() }() // read-only

	if *outreachMode {
		return runGapOutreach(ctx, st, *sep, ruleset, *limit, cfg.JSON, stdout, stderr, jsonMode)
	}
	page, err := st.Gap(ctx, *sep, ruleset, *limit, *cursor)
	if err != nil {
		return fail(stderr, jsonMode, "gap", err)
	}
	rows := page.Contracts
	if cfg.JSON {
		b, err := json.MarshalIndent(gapOutput{SEP: *sep, Tier: "inferred", RulesetVersion: ruleset,
			ParserVersion: sepmeta.ParserVersion, Count: len(rows), Contracts: rows, NextCursor: page.NextCursor}, "", "  ")
		if err != nil {
			return fail(stderr, jsonMode, "gap", err)
		}
		if _, err := fmt.Fprintln(stdout, string(b)); err != nil {
			return fail(stderr, jsonMode, "gap", err)
		}
		return exitOK
	}
	if _, err := fmt.Fprintf(stdout, "Undeclared gap for SEP-%d (inferred: interface matches %s, no SEP declared): %d contracts\n",
		*sep, ruleset, len(rows)); err != nil {
		return fail(stderr, jsonMode, "gap", err)
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(stdout, "%s  %s\n", r.ContractID, r.WasmHash); err != nil {
			return fail(stderr, jsonMode, "gap", err)
		}
	}
	if page.NextCursor != nil {
		if _, err := fmt.Fprintf(stderr, "next cursor: %s\n", *page.NextCursor); err != nil {
			return fail(stderr, jsonMode, "gap", err)
		}
	}
	return exitOK
}

// runGapOutreach groups the inferred gap and renders the suggestions. The
// output never presents a group as a declaration and never names a project:
// the index only stores contract IDs and Wasm hashes.
func runGapOutreach(ctx context.Context, st *store.Store, sep int, ruleset string, limit int, asJSON bool, stdout, stderr io.Writer, jsonMode bool) int {
	groups, totalGroups, totalContracts, err := st.GapGroups(ctx, sep, ruleset, limit)
	if err != nil {
		return fail(stderr, jsonMode, "gap", err)
	}
	out := make([]outreach.Group, 0, len(groups))
	for _, g := range groups {
		out = append(out, outreach.Group{WasmHash: g.WasmHash, OKCount: g.OKCount, Contracts: g.Contracts})
	}
	out = outreach.GroupOutputs(sep, ruleset, out)
	if asJSON {
		b, err := json.MarshalIndent(outreachOutput{SEP: sep, Tier: "inferred", RulesetVersion: ruleset,
			ParserVersion: sepmeta.ParserVersion, Groups: out, TotalGroups: totalGroups, TotalContracts: totalContracts}, "", "  ")
		if err != nil {
			return fail(stderr, jsonMode, "gap", err)
		}
		if _, err := fmt.Fprintln(stdout, string(b)); err != nil {
			return fail(stderr, jsonMode, "gap", err)
		}
		return exitOK
	}
	if _, err := io.WriteString(stdout, outreach.Markdown(sep, ruleset, out, totalGroups, totalContracts)); err != nil {
		return fail(stderr, jsonMode, "gap", err)
	}
	return exitOK
}
