package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/stellar/go-stellar-sdk/strkey"
)

func TestContractCLIPositionalFlagsAndNotFound(t *testing.T) {
	db, id, _ := readFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	for _, args := range [][]string{{"contract", id, "--json"}, {"contract", "--json", id}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 0 {
			t.Fatal(code, errb.String())
		}
		var d store.ContractDetail
		if err := json.Unmarshal(out.Bytes(), &d); err != nil || d.ContractID != id || !d.Claims[0].Declared {
			t.Fatal(out.String())
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"contract", id, "--history", "--json"}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	var h store.HistoryDetail
	if err := json.Unmarshal(out.Bytes(), &h); err != nil || len(h.History) != 1 || h.History[0].FromLedger != 10 {
		t.Fatal(out.String())
	}
	b := make([]byte, 32)
	b[0] = 1
	missing, err := strkey.Encode(strkey.VersionByteContract, b)
	if err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"contract", missing}, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatal(code)
	}
	for _, args := range [][]string{{"contract", "invalid"}, {"contract"}, {"contract", id, "extra"}} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
			t.Fatal(args, code)
		}
	}
}

func TestContractCLIHumanEvidenceRendering(t *testing.T) {
	db, id, hash := readFixture(t)
	ctx := context.Background()
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mism := `[{"fn":"transfer","reason":"types","expected":{"inputs":["Address","MuxedAddress","i128"],"outputs":[]},"actual":{"inputs":["Address","Address"],"outputs":[]}}]`
	if err = tx.UpsertMatch(ctx, store.MatchRow{
		Hash:                 hash,
		SEP:                  41,
		RulesetVersion:       "sep41-v0.5.2",
		Status:               "partial",
		OKCount:              8,
		MissingJSON:          `["burn","burn_from"]`,
		MismatchedJSON:       mism,
		MatchedVariantsJSON:  "{}",
		ComputedAt:           "2026-11-09T00:00:00Z",
	}); err != nil {
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
	if code := run([]string{"contract", id}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	for _, want := range []string{
		"SEP-41: declared=true, inferred=partial (sep41-v0.5.2), verified=not run, protocol=false\n",
		"  Missing: burn, burn_from\n",
		"  Mismatched transfer: expected (Address, MuxedAddress, i128) -> (), found (Address, Address) -> ()\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestContractCLINoSpecWasm(t *testing.T) {
	db, id, hash := readFixture(t)
	ctx := context.Background()
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.UpsertMatch(ctx, store.MatchRow{
		Hash:                 hash,
		SEP:                  41,
		RulesetVersion:       "sep41-v0.5.2",
		Status:               "no_spec",
		OKCount:              0,
		MissingJSON:          "[]",
		MismatchedJSON:       "[]",
		MatchedVariantsJSON:  "{}",
		ComputedAt:           "2026-11-09T00:00:00Z",
	}); err != nil {
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
	if code := run([]string{"contract", id}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	for _, want := range []string{
		"inferred=no_spec (sep41-v0.5.2)",
		"  No spec: Wasm has no contractspecv0 section",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "Missing:") || strings.Contains(out.String(), "Mismatched") {
		t.Fatalf("unexpected missing/mismatched output for no_spec:\n%s", out.String())
	}
}

func TestContractCLINoMatchRow(t *testing.T) {
	db, id, _ := readFixture(t)
	// Default fixture has declared claim SEP-41 but NO row in interface_matches
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"contract", id}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	want := "inferred=no match row"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("missing %q in:\n%s", want, out.String())
	}
}

func TestContractCLIUnresolvedAndArchivedExecRef(t *testing.T) {
	db, _, _ := readFixture(t)
	ctx := context.Background()
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 32)
	b[1] = 2
	refContractID, err := strkey.Encode(strkey.VersionByteContract, b)
	if err != nil {
		t.Fatal(err)
	}
	b[2] = 3
	ownerID, err := strkey.Encode(strkey.VersionByteContract, b)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.UpsertContract(ctx, store.ContractRow{
		ID:              refContractID,
		Kind:            "wasm_ref",
		CurrentWasmHash: "",
		ExecRefOwner:    ownerID,
		ExecRefTag:      "v1",
		Archived:        false,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.SetVersion(ctx, store.Version{
		ContractID:   refContractID,
		ExecRefOwner: ownerID,
		ExecRefTag:   "v1",
		FromLedger:   15,
	}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SEP47IDX_DB", db)

	// 1. Unresolved, not archived
	var out, errb bytes.Buffer
	if code := run([]string{"contract", refContractID}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	for _, want := range []string{
		"(unresolved reference)",
		"Unresolved executable reference: reference entry not found.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}

	// 2. Unresolved, archived
	s, err = store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	tx, err = s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.UpsertContract(ctx, store.ContractRow{
		ID:              refContractID,
		Kind:            "wasm_ref",
		CurrentWasmHash: "",
		ExecRefOwner:    ownerID,
		ExecRefTag:      "v1",
		Archived:        true,
	}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errb.Reset()
	if code := run([]string{"contract", refContractID}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	for _, want := range []string{
		"(archived reference)",
		"Unresolved executable reference: reference entry is archived.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
}
