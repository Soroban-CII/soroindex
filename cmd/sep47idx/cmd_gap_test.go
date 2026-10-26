package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/stellar/go-stellar-sdk/strkey"
)

func gapFixture(t *testing.T, count int) (string, []string, string) {
	t.Helper()
	db, id, hash := readFixture(t)
	ctx := context.Background()
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := tx.ReplaceClaims(ctx, hash, "sep47-meta", nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.UpsertMatch(ctx, store.MatchRow{Hash: hash, SEP: 41, RulesetVersion: "sep41-v0.5.2", Status: "match", OKCount: 10,
		MissingJSON: "[]", MismatchedJSON: "[]", MatchedVariantsJSON: "{}", ComputedAt: "2026-10-26T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	ids := []string{id}
	for i := 1; i < count; i++ {
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
	sort.Strings(ids)
	return db, ids, hash
}

func TestGapDefaultOutputCompatibility(t *testing.T) {
	db, ids, hash := gapFixture(t, 1)
	t.Setenv("SEP47IDX_DB", db)
	var out, stderr bytes.Buffer
	if code := run([]string{"gap"}, &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatal(code, stderr.String())
	}
	want := fmt.Sprintf("Undeclared gap for SEP-41 (inferred: interface matches sep41-v0.5.2, no SEP declared): 1 contracts\n%s  %s\n", ids[0], hash)
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
	out.Reset()
	if code := run([]string{"gap", "--json"}, &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatal(code, stderr.String())
	}
	want = fmt.Sprintf(`{
  "sep": 41,
  "tier": "inferred",
  "ruleset_version": "sep41-v0.5.2",
  "parser_version": %q,
  "count": 1,
  "contracts": [
    {
      "contract_id": %q,
      "wasm_hash": %q,
      "ok_count": 10
    }
  ]
}
`, sepmeta.ParserVersion, ids[0], hash)
	if out.String() != want {
		t.Fatalf("got %s, want %s", &out, want)
	}
}

func TestGapCLIContinuationAndEnvironment(t *testing.T) {
	db, ids, _ := gapFixture(t, 503)
	t.Setenv("SEP47IDX_DB", db)
	var out, stderr bytes.Buffer
	if code := run([]string{"gap", "--json"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	var page gapOutput
	if err := json.Unmarshal(out.Bytes(), &page); err != nil || page.Count != 50 || len(page.Contracts) != 50 || page.NextCursor == nil || stderr.Len() != 0 {
		t.Fatal(page, err, stderr.String())
	}
	var got []string
	cursor := ""
	for {
		out.Reset()
		stderr.Reset()
		if code := run([]string{"gap", "--limit", "500", "--cursor", cursor, "--json"}, &out, &stderr); code != 0 {
			t.Fatal(code, stderr.String())
		}
		page = gapOutput{}
		if err := json.Unmarshal(out.Bytes(), &page); err != nil || page.Count != len(page.Contracts) || page.Count > 500 || page.Tier != "inferred" || page.RulesetVersion != "sep41-v0.5.2" || page.ParserVersion != sepmeta.ParserVersion {
			t.Fatal(page, err)
		}
		for _, r := range page.Contracts {
			got = append(got, r.ContractID)
		}
		if page.NextCursor == nil {
			break
		}
		if *page.NextCursor == cursor || len(got) >= len(ids) {
			t.Fatal("cursor did not advance", page)
		}
		cursor = *page.NextCursor
	}
	if strings.Join(got, ",") != strings.Join(ids, ",") {
		t.Fatal("paged result differs from sorted gap", len(got), len(ids))
	}
	out.Reset()
	stderr.Reset()
	t.Setenv("SEP47IDX_LIMIT", "1")
	if code := run([]string{"gap"}, &out, &stderr); code != 0 || strings.Count(out.String(), "\n") != 2 || !strings.HasPrefix(stderr.String(), "next cursor: ") {
		t.Fatal(code, out.String(), stderr.String())
	}
	t.Setenv("SEP47IDX_CURSOR", strings.TrimSpace(strings.TrimPrefix(stderr.String(), "next cursor: ")))
	out.Reset()
	stderr.Reset()
	if code := run([]string{"gap", "--limit", "500", "--json"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	page = gapOutput{}
	if err := json.Unmarshal(out.Bytes(), &page); err != nil || page.Count != 500 || page.Contracts[0].ContractID != ids[1] {
		t.Fatal(page, err)
	}
	out.Reset()
	if code := run([]string{"gap", "--limit", "500", "--cursor", "", "--json"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	page = gapOutput{}
	if err := json.Unmarshal(out.Bytes(), &page); err != nil || page.Contracts[0].ContractID != ids[0] {
		t.Fatal(page, err)
	}
}

func TestGapCLIErrorsAndEmptyPage(t *testing.T) {
	db, _, _ := gapFixture(t, 2)
	t.Setenv("SEP47IDX_DB", db)
	for _, args := range [][]string{
		{"gap", "extra"}, {"gap", "--limit", "0"}, {"gap", "--limit", "501"},
		{"gap", "--sep", "0"}, {"gap", "--sep", "1000000"}, {"gap", "--cursor", "bad"},
	} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
			t.Fatal(args, code)
		}
	}
	if code := run([]string{"gap", "--sep", "50"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatal("unknown ruleset did not exit 2", code)
	}
	for _, args := range [][]string{{"gap"}, {"gap", "--json"}} {
		if code := run(args, failWriter{}, &bytes.Buffer{}); code != 1 {
			t.Fatal("output failure ignored", args, code)
		}
	}
	if code := run([]string{"gap", "--limit", "1"}, &bytes.Buffer{}, failWriter{}); code != 1 {
		t.Fatal("cursor output failure ignored", code)
	}
	var out bytes.Buffer
	if code := run([]string{"gap", "--limit", "2", "--json"}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatal(code)
	}
	var page gapOutput
	if err := json.Unmarshal(out.Bytes(), &page); err != nil || page.NextCursor != nil {
		t.Fatal(page, err)
	}
	emptyDB, _, _ := readFixture(t) // Its only contract declares a SEP, so the gap is empty.
	out.Reset()
	if code := run([]string{"gap", "--db", emptyDB, "--json"}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatal(code)
	}
	page = gapOutput{}
	if err := json.Unmarshal(out.Bytes(), &page); err != nil || page.Count != 0 || page.Contracts == nil || len(page.Contracts) != 0 || page.NextCursor != nil {
		t.Fatal(page, err)
	}
}
