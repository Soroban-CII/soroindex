package ingest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/store"
)

// TestRecomputeOnlyStaleRows is the step 22 test: bumping ruleset_version
// recomputes only the rows whose stored version differs, from
// functions_json, with no RPC calls.
func TestRecomputeOnlyStaleRows(t *testing.T) {
	ctx := context.Background()
	fullCode, full := wasmFixture(t, "token_full_sep.wasm")
	partialCode, partial := wasmFixture(t, "token_partial.wasm")
	f := &entryRPC{t: t, latest: 100, code: map[string][]byte{full: fullCode, partial: partialCode}}
	ix, s := newIndexer(t, f)

	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.EnsureWasm(ctx, tx, []string{full, partial}, 1); err != nil {
		t.Fatal(err)
	}
	// Two Wasm rows with no spec and no functions: one parsed, one never parsed.
	if err := tx.UpsertWasm(ctx, store.WasmRow{Hash: strings.Repeat("aa", 32), ParseStatus: "ok", ParserVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.UpsertWasm(ctx, store.WasmRow{Hash: strings.Repeat("bb", 32), ParseStatus: "archived", ParserVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	// One Wasm analyzed "before migration 0002": a spec but no functions_json.
	if err := tx.UpsertWasm(ctx, store.WasmRow{Hash: strings.Repeat("cc", 32), HasSpec: true, ParseStatus: "ok", ParserVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	callsAfterFetch := f.calls

	// Bump the ruleset version and make the bump change an outcome: the new
	// rules drop burn_from, so token_full_sep matches 9 of 9.
	bumped := ix.Rules[0]
	bumped.RulesetVersion = "sep41-v0.5.2-r1"
	var req []match.FnRule
	for _, r := range bumped.Required {
		if r.Fn == "burn_from" {
			continue
		}
		req = append(req, r)
	}
	bumped.Required = req
	ix.Rules = []match.RuleFile{bumped}

	// partial already has a row under the new version: it must not change.
	pre := `INSERT INTO interface_matches VALUES (?, 41, 'sep41-v0.5.2-r1', 'mismatch', 0, '[]', '[]', '{}', '2000-01-01T00:00:00Z')`
	if _, err := s.DB.Exec(pre, partial); err != nil {
		t.Fatal(err)
	}

	st, err := ix.Recompute(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := RecomputeStats{Recomputed: 1, NoSpec: 1, NeedsRefetch: 1, NeverParsed: 1}
	if st != want {
		t.Fatalf("stats = %+v, want %+v", st, want)
	}
	if f.calls != callsAfterFetch {
		t.Fatalf("recompute made %d RPC calls", f.calls-callsAfterFetch)
	}
	var status, missing string
	var ok int
	if err := s.DB.QueryRow(`SELECT status, ok_count, missing_json FROM interface_matches WHERE wasm_hash = ? AND ruleset_version = 'sep41-v0.5.2-r1'`, full).Scan(&status, &ok, &missing); err != nil {
		t.Fatal(err)
	}
	if status != "match" || ok != 9 || missing != "[]" {
		t.Fatalf("full under new rules = %s %d %s, want match 9/9", status, ok, missing)
	}
	var computedAt string
	if err := s.DB.QueryRow(`SELECT computed_at FROM interface_matches WHERE wasm_hash = ? AND ruleset_version = 'sep41-v0.5.2-r1'`, partial).Scan(&computedAt); err != nil || computedAt != "2000-01-01T00:00:00Z" {
		t.Fatalf("an up-to-date row was recomputed: computed_at %q, %v", computedAt, err)
	}
	var old int
	if err := s.DB.QueryRow(`SELECT count(*) FROM interface_matches WHERE ruleset_version = 'sep41-v0.5.2'`).Scan(&old); err != nil || old != 2 {
		t.Fatalf("rows under the old version = %d, %v; history must be kept", old, err)
	}
	if v, _ := s.State(ctx, store.KeyRulesetVersions); v != "sep41-v0.5.2-r1" {
		t.Fatalf("ruleset_versions = %q", v)
	}
	st2, err := ix.Recompute(ctx)
	if err != nil || st2.Recomputed != 0 || st2.NoSpec != 0 {
		t.Fatalf("second recompute did work: %+v, %v", st2, err)
	}
}

func TestFunctionsJSONWrittenAndKeptOnArchival(t *testing.T) {
	ctx := context.Background()
	code, h := wasmFixture(t, "not_token.wasm")
	f := &entryRPC{t: t, latest: 100, code: map[string][]byte{h: code}}
	ix, s := newIndexer(t, f)
	tx, _ := s.Begin(ctx)
	if _, err := ix.EnsureWasm(ctx, tx, []string{h}, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.UpsertWasm(ctx, store.WasmRow{Hash: h, ParseStatus: ParseArchived, ParserVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.DB.QueryRow(`SELECT functions_json FROM wasm WHERE hash = ?`, h).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var fns []struct{ Name string }
	if err := json.Unmarshal([]byte(raw), &fns); err != nil || len(fns) != 3 {
		t.Fatalf("functions_json = %s (%v), want not_token's 3 functions kept after archival", raw, err)
	}
}
