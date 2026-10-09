package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/rules"
)

func loadedRulesets() (map[int]string, error) {
	files, err := match.LoadRules(rules.FS)
	if err != nil {
		return nil, err
	}
	versions := map[int]string{}
	for _, r := range files {
		versions[r.SEP] = r.RulesetVersion
	}
	return versions, nil
}

func readIndex(cfg config.Common, action func(context.Context, *store.Store, map[int]string) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.RPCTimeout)
	defer cancel()
	versions, err := loadedRulesets()
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, cfg.DB, store.Options{Passphrase: cfg.Network.Passphrase, ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	return action(ctx, s, versions)
}

func readExit(name string, err error, stderr io.Writer) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	errorf(stderr, "sep47idx %s: %v\n", name, err)
	if errors.Is(err, store.ErrNotFound) {
		return exitNotFound
	}
	return exitError
}

func jsonOutput(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// parseObject accepts the documented ID-first syntax and flag-first syntax.
func parseObject(fs *flag.FlagSet, args []string) (string, error) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id := args[0]
		if err := fs.Parse(args[1:]); err != nil {
			return "", err
		}
		if fs.NArg() != 0 {
			return "", fmt.Errorf("exactly one identifier is required")
		}
		return id, nil
	}
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 1 {
		return "", fmt.Errorf("exactly one identifier is required")
	}
	return fs.Arg(0), nil
}
