package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
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

func TestQueryCSVAllPagesAndResume(t *testing.T) {
	db, firstID, hash := readFixture(t)
	ctx := context.Background()
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{firstID}
	for i := 1; i <= 602; i++ {
		raw := make([]byte, 32)
		binary.BigEndian.PutUint32(raw, uint32(i))
		id, err := strkey.Encode(strkey.VersionByteContract, raw)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id, Kind: "wasm", CurrentWasmHash: hash}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	t.Setenv("SEP47IDX_DB", db)
	var out, stderr bytes.Buffer
	args := []string{"query", "--implements", "41", "--tier", "declared", "--limit", "500", "--csv", "--all"}
	if code := run(args, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	rows, err := csv.NewReader(&out).ReadAll()
	if err != nil || len(rows) != 604 || stderr.Len() != 0 {
		t.Fatal(len(rows), err, stderr.String())
	}
	for i, id := range ids {
		if rows[i+1][0] != id || rows[i+1][6] != "declared" {
			t.Fatalf("row %d: %v", i, rows[i+1])
		}
	}
	out.Reset()
	stderr.Reset()
	if code := run([]string{"query", "--tier", "declared", "--limit", "500", "--json"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	var page store.Page
	if err := json.Unmarshal(out.Bytes(), &page); err != nil || page.NextCursor == nil {
		t.Fatal(err, out.String())
	}
	out.Reset()
	if code := run(append(args, "--cursor", *page.NextCursor), &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	rows, err = csv.NewReader(&out).ReadAll()
	if err != nil || len(rows) != 104 {
		t.Fatal(len(rows), err)
	}
	for i, id := range ids[500:] {
		if rows[i+1][0] != id {
			t.Fatal(i, rows[i+1])
		}
	}
	out.Reset()
	stderr.Reset()
	if code := run([]string{"query", "--tier", "declared", "--limit", "500", "--csv"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	rows, err = csv.NewReader(&out).ReadAll()
	if err != nil || len(rows) != 501 || !strings.Contains(stderr.String(), "next cursor:") {
		t.Fatal(len(rows), err, stderr.String())
	}
	if code := run([]string{"query", "--all"}, &out, &stderr); code != 1 {
		t.Fatal("--all without CSV accepted")
	}
	if code := run(args, failWriter{}, &bytes.Buffer{}); code != 1 {
		t.Fatal("CSV output error ignored")
	}
}

func TestCSVExportStopsOnQueryFailureCancellationAndOutputFailure(t *testing.T) {
	cursor := "next-page"
	page := store.Page{Tier: "declared", Contracts: []store.ContractSummary{{ID: "first", Kind: "wasm"}}, NextCursor: &cursor}
	queryError := errors.New("query failed")
	var out bytes.Buffer
	calls := 0
	query := func(_ context.Context, f store.Filter) (store.Page, error) {
		calls++
		if f.Cursor != cursor || f.Limit != 2 || f.Tier != "declared" {
			t.Fatal(f)
		}
		return store.Page{}, queryError
	}
	filter := store.Filter{Limit: 2, Tier: "declared"}
	err := exportQueryCSV(context.Background(), &out, page, filter, true, query)
	if !errors.Is(err, queryError) || calls != 1 || !strings.Contains(err.Error(), cursor) {
		t.Fatal(err, calls)
	}
	rows, err := csv.NewReader(&out).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	calls = 0
	if err := exportQueryCSV(context.Background(), failWriter{}, page, filter, true, query); err == nil || calls != 0 {
		t.Fatal(err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	writer := &cancelCSVWriter{cancel: cancel}
	if err := exportQueryCSV(ctx, writer, page, filter, true, query); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal(err, calls)
	}
	if rows, err := csv.NewReader(&writer.Buffer).ReadAll(); err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	out.Reset()
	if err := exportQueryCSV(ctx, &out, page, filter, true, query); !errors.Is(err, context.Canceled) || calls != 0 || out.Len() != 0 {
		t.Fatal(err, calls, out.String())
	}
}

type cancelCSVWriter struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelCSVWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.cancel()
	return n, err
}
