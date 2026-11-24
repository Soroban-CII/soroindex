package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/Soroban-CII/soroindex/internal/claims"
	"github.com/Soroban-CII/soroindex/internal/clierr"
	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
)

func init() {
	commands["inspect"] = command{summary: "analyze a local Wasm file offline, as the indexer would", run: runInspect}
}

// runInspect runs the indexer's own analysis over a local file. It opens no
// database and makes no RPC call, so authors can check a build before
// deploying it. The file is parsed, never executed.
func runInspect(args []string, stdout, stderr io.Writer) int {
	jsonMode := jsonRequested(args)
	fs := newFlagSet("inspect", stderr, jsonMode)
	asJSON := fs.Bool("json", false, "print JSON (env SEP47IDX_JSON)")
	require := fs.Int("require-match", 0, "exit 1 unless the inferred status for this SEP is match (env SEP47IDX_REQUIRE_MATCH)")
	threshold := fs.Float64("partial-threshold", match.DefaultPartialThreshold, "match.partial_threshold, as for sync")
	path, err := parseObject(fs, args)
	if err == nil {
		err = config.ApplyEnv(fs, os.LookupEnv)
	}
	if err != nil {
		return readExit("inspect", err, stderr, jsonMode)
	}
	if *require < 0 {
		return readExit("inspect", clierr.Newf("bad_request", "--require-match must be a positive SEP number"), stderr, jsonMode)
	}
	lim := sepmeta.DefaultLimits()
	code, err := readWasmFile(path, lim.MaxWasmBytes)
	if err != nil {
		return readExit("inspect", err, stderr, jsonMode)
	}
	ruleFiles, err := match.LoadRules(rules.FS)
	if err != nil {
		return readExit("inspect", err, stderr, jsonMode)
	}
	if *require > 0 && !hasRule(ruleFiles, *require) {
		return readExit("inspect", clierr.Newf("bad_request", "--require-match %d: no rule file for SEP-%d", *require, *require), stderr, jsonMode)
	}
	versions, err := match.LoadVersions(rules.FS, ruleFiles)
	if err != nil {
		return readExit("inspect", err, stderr, jsonMode)
	}
	ix := &ingest.Indexer{Rules: ruleFiles, Matcher: match.Matcher{PartialThreshold: *threshold}, Limits: lim,
		Claims: claims.Default(lim), Log: slog.New(slog.NewJSONHandler(stderr, nil)), Now: time.Now, Versions: versions.Highest}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := ix.Inspect(ctx, code)
	if err != nil {
		return readExit("inspect", err, stderr, jsonMode)
	}
	if *asJSON {
		err = jsonOutput(stdout, e)
	} else {
		err = writeInspect(stdout, path, e)
	}
	if err != nil {
		return readExit("inspect", err, stderr, jsonMode)
	}
	if *require > 0 {
		for _, m := range e.Matches {
			if m.SEP == *require && m.Status != string(match.StatusMatch) {
				return readExit("inspect", clierr.Newf("not_matched", "required interface does not match: SEP-%d is %s", m.SEP, m.Status), stderr, jsonMode)
			}
		}
	}
	return exitOK
}

// readWasmFile reads at most limit bytes. A larger file is refused before it
// is read into memory: the indexer's total Wasm limit would reject it too.
func readWasmFile(path string, limit int) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 G703 -- the operator names the local file to inspect; it is only read
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only handle
	code, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(code) > limit {
		return nil, fmt.Errorf("%s: %w: larger than the %d-byte total Wasm limit", path, sepmeta.ErrLimit, limit)
	}
	return code, nil
}

func hasRule(files []match.RuleFile, sep int) bool {
	for _, rf := range files {
		if rf.SEP == sep {
			return true
		}
	}
	return false
}

func writeInspect(w io.Writer, path string, e ingest.Evidence) error {
	if _, err := fmt.Fprintf(w, "%s: %s (parser %s), local file %s, not indexed\n", e.Hash, e.ParseStatus, e.ParserVersion, path); err != nil {
		return err
	}
	return writeEvidence(w, e.Meta, e.ParseError, e.Claims, e.Anomalies, e.Matches)
}
