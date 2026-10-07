package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/match"
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
}

// runGap implements `sep47idx gap --sep 41 [--json]` (CLAUDE.md §5.12).
func runGap(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gap", flag.ContinueOnError)
	flags.SetOutput(stderr)
	resolve := config.RegisterCommon(flags)
	sep := flags.Int("sep", 41, "SEP number")
	if err := flags.Parse(args); err != nil {
		return exitError
	}
	cfg, err := resolve()
	if err != nil {
		errorf(stderr, "sep47idx gap: %v\n", err)
		return exitError
	}
	ruleFiles, err := match.LoadRules(rules.FS)
	if err != nil {
		errorf(stderr, "sep47idx gap: rules: %v\n", err)
		return exitError
	}
	ruleset := ""
	for _, r := range ruleFiles {
		if r.SEP == *sep {
			ruleset = r.RulesetVersion
		}
	}
	if ruleset == "" {
		errorf(stderr, "sep47idx gap: no rule file for SEP-%d, so no interface can be inferred\n", *sep)
		return exitNotFound
	}
	if _, err := os.Stat(cfg.DB); err != nil {
		errorf(stderr, "sep47idx gap: %v (run sync first)\n", err)
		return exitError
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DB, store.Options{Passphrase: cfg.Network.Passphrase, ReadOnly: true})
	if err != nil {
		errorf(stderr, "sep47idx gap: %v\n", err)
		return exitError
	}
	defer func() { _ = st.Close() }() // read-only
	rows, err := st.Gap(ctx, *sep, ruleset)
	if err != nil {
		errorf(stderr, "sep47idx gap: %v\n", err)
		return exitError
	}
	if cfg.JSON {
		b, err := json.MarshalIndent(gapOutput{SEP: *sep, Tier: "inferred", RulesetVersion: ruleset,
			ParserVersion: sepmeta.ParserVersion, Count: len(rows), Contracts: rows}, "", "  ")
		if err != nil {
			return exitError
		}
		if _, err := fmt.Fprintln(stdout, string(b)); err != nil {
			return exitError
		}
		return exitOK
	}
	if _, err := fmt.Fprintf(stdout, "Undeclared gap for SEP-%d (inferred: interface matches %s, no SEP declared): %d contracts\n",
		*sep, ruleset, len(rows)); err != nil {
		return exitError
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(stdout, "%s  %s\n", r.ContractID, r.WasmHash); err != nil {
			return exitError
		}
	}
	return exitOK
}
