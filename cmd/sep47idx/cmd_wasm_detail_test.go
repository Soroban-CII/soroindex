package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
)

func TestWasmCLIJSONAndValidation(t *testing.T) {
	db, _, hash := readFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"wasm", hash, "--json"}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	var d store.WasmDetail
	if err := json.Unmarshal(out.Bytes(), &d); err != nil || len(d.Claims) != 1 || d.Hash != hash {
		t.Fatal(out.String())
	}
	if code := run([]string{"wasm", strings.Repeat("b", 64)}, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatal(code)
	}
	for _, args := range [][]string{{"wasm", strings.ToUpper(hash)}, {"wasm", "oops"}, {"wasm", hash, "--limit", "0"}, {"wasm", hash, "--cursor", "bad"}} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
			t.Fatal(args, code)
		}
	}
}

func TestWasmCLIHumanOutputNamesWhatToFix(t *testing.T) {
	db, _, hash := readFixture(t)
	ctx := context.Background()
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mism := `[{"fn":"transfer","reason":"types","expected":{"inputs":["Address","MuxedAddress","i128"],"outputs":[]},"actual":{"inputs":["Address","Address"],"outputs":[]}},` +
		`{"fn":"balance","reason":"duplicate","expected":{"inputs":["Address"],"outputs":["i128"]},"actual":{"inputs":["Address"],"outputs":["i128"]}}]`
	if err = tx.UpsertMatch(ctx, store.MatchRow{Hash: hash, SEP: 41, RulesetVersion: "sep41-v0.5.2", Status: "partial", OKCount: 6,
		MissingJSON: `["burn","burn_from"]`, MismatchedJSON: mism, MatchedVariantsJSON: "{}", ComputedAt: "2026-11-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"wasm", hash}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	for _, want := range []string{
		"Inferred SEP-41: partial (sep41-v0.5.2), 6 functions matched\n  Missing: burn, burn_from\n",
		"  Mismatched transfer: expected (Address, MuxedAddress, i128) -> (), found (Address, Address) -> ()\n",
		"  Mismatched balance: defined more than once\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestWasmCLICompleteMatchListsNothingToFix(t *testing.T) {
	db, _, hash := readFixture(t)
	ctx := context.Background()
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.UpsertMatch(ctx, store.MatchRow{Hash: hash, SEP: 41, RulesetVersion: "sep41-v0.5.2", Status: "match", OKCount: 10,
		MissingJSON: "[]", MismatchedJSON: "[]", MatchedVariantsJSON: `{"transfer.inputs[1]":"MuxedAddress"}`, ComputedAt: "2026-11-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"wasm", hash}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	if strings.Contains(out.String(), "Missing:") || strings.Contains(out.String(), "Mismatched") {
		t.Fatalf("complete match lists fixes:\n%s", out.String())
	}
}

// TestMatchEvidenceFromRealMatcher runs the shipped SEP-41 rules over the
// committed token_partial.wasm fixture, which lacks burn and burn_from,
// stores the result the way the indexer does, and renders it.
func TestMatchEvidenceFromRealMatcher(t *testing.T) {
	wasm, err := os.ReadFile("../../testdata/wasm/token_partial.wasm")
	if err != nil {
		t.Fatal(err)
	}
	sections, err := sepmeta.ReadCustomSections(wasm, []string{sepmeta.SectionSpec}, sepmeta.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	spec, err := sepmeta.DecodeSpec(sections[sepmeta.SectionSpec], sepmeta.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	ruleFiles, err := match.LoadRules(rules.FS)
	if err != nil {
		t.Fatal(err)
	}
	var sep41 *match.RuleFile
	for i := range ruleFiles {
		if ruleFiles[i].SEP == 41 {
			sep41 = &ruleFiles[i]
		}
	}
	if sep41 == nil {
		t.Fatal("no SEP-41 rule file")
	}
	res := match.Match(sepmeta.Functions(spec), *sep41)
	missing, err := json.Marshal(res.Missing)
	if err != nil {
		t.Fatal(err)
	}
	mism, err := json.Marshal(res.Mismatched)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = writeMatchEvidence(&out, store.WasmMatch{SEP: 41, Missing: missing, Mismatched: mism}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "  Missing: burn, burn_from\n" {
		t.Fatalf("status %s, evidence:\n%s", res.Status, out.String())
	}
}

func TestMatchEvidenceRejectsCorruptStoredJSON(t *testing.T) {
	for _, m := range []store.WasmMatch{
		{SEP: 41, Missing: json.RawMessage(`{`), Mismatched: json.RawMessage(`[]`)},
		{SEP: 41, Missing: json.RawMessage(`[]`), Mismatched: json.RawMessage(`"x"`)},
	} {
		if err := writeMatchEvidence(&bytes.Buffer{}, m); err == nil {
			t.Fatalf("accepted %s / %s", m.Missing, m.Mismatched)
		}
	}
}
