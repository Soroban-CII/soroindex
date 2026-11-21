package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// inferredFixture creates a database with a contract that has an interface
// match row so that printClaims can show the evidence.
func inferredFixture(t *testing.T, status, missingJSON, mismatchedJSON string) (dbPath, contractID string) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "inferred.db")
	ctx := context.Background()
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	id, err := strkey.Encode(strkey.VersionByteContract, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("b", 64)
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.UpsertWasm(ctx, store.WasmRow{Hash: hash, MetaJSON: "[]", ParseStatus: "ok", ParserVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if err = tx.UpsertContract(ctx, store.ContractRow{ID: id, Kind: "wasm", CurrentWasmHash: hash}); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.SetVersion(ctx, store.Version{ContractID: id, WasmHash: hash, FromLedger: 10}); err != nil {
		t.Fatal(err)
	}
	if err = tx.ReplaceClaims(ctx, hash, "sep47-meta", []store.ClaimRow{{SEP: 41, RawToken: "41"}}); err != nil {
		t.Fatal(err)
	}
	if err = tx.UpsertMatch(ctx, store.MatchRow{
		Hash: hash, SEP: 41, RulesetVersion: "sep41-v0.5.2",
		Status: status, OKCount: 8,
		MissingJSON:         missingJSON,
		MismatchedJSON:      mismatchedJSON,
		MatchedVariantsJSON: "{}",
		ComputedAt:          time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	if err = tx.SetLastLedger(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db, id
}

func TestContractCLIInferredEvidence(t *testing.T) {
	missingJSON := `["burn","burn_from"]`
	mismatchedJSON := `[{"fn":"transfer","expected":{"inputs":["Address","MuxedAddress","i128"],"outputs":[]},"actual":{"inputs":["Address","Address","i128"],"outputs":[]},"optional":false,"reason":""}]`
	db, id := inferredFixture(t, "partial", missingJSON, mismatchedJSON)
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"contract", id}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "Missing: burn, burn_from") {
		t.Errorf("missing evidence line not found in output:\n%s", got)
	}
	if !strings.Contains(got, "Mismatched transfer") {
		t.Errorf("mismatched evidence line not found in output:\n%s", got)
	}
}

func TestContractCLINoSpecEvidence(t *testing.T) {
	db, id := inferredFixture(t, "no_spec", `[]`, `[]`)
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"contract", id}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "no spec section") {
		t.Errorf("no_spec note not found in output:\n%s", got)
	}
}

func TestContractCLIMatchEvidence(t *testing.T) {
	db, id := inferredFixture(t, "match", `[]`, `[]`)
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"contract", id}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	got := out.String()
	if strings.Contains(got, "Missing:") || strings.Contains(got, "Mismatched") {
		t.Errorf("unexpected evidence lines for match status:\n%s", got)
	}
}
