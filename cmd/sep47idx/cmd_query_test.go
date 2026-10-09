package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/stellar/go-stellar-sdk/strkey"
)

func readFixture(t *testing.T) (string, string, string) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "testnet.db")
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
	hash := strings.Repeat("a", 64)
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
	if err = tx.SetLastLedger(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db, id, hash
}

func TestQueryCLIJSONCSVAndErrors(t *testing.T) {
	db, id, _ := readFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	t.Setenv("SEP47IDX_TIER", "protocol")
	var out, errb bytes.Buffer
	if code := run([]string{"query", "--implements", "41", "--tier", "declared", "--json"}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	var p store.Page
	if err := json.Unmarshal(out.Bytes(), &p); err != nil || len(p.Contracts) != 1 || p.Contracts[0].ID != id {
		t.Fatal(out.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"query", "--tier", "declared", "--csv"}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	rows, err := csv.NewReader(&out).ReadAll()
	if err != nil || len(rows) != 2 || rows[1][0] != id || rows[1][6] != "declared" {
		t.Fatal(rows, err)
	}
	for _, args := range [][]string{{"query", "--json", "--csv"}, {"query", "--implements", "41,"}, {"query", "--limit", "0"}, {"query", "extra"}, {"query", "--tier", "unknown"}} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
			t.Fatal(args, code)
		}
	}
	if code := run([]string{"query", "--tier", "declared"}, failWriter{}, &bytes.Buffer{}); code != 1 {
		t.Fatal("output error ignored")
	}
}
