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

func TestGapOutreachGroupsByHashAndNamesNoProject(t *testing.T) {
	db, ids, hash := gapFixture(t, 1)
	ctx := context.Background()
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	factoryHash := strings.Repeat("b", 64)
	if err := tx.UpsertMatch(ctx, store.MatchRow{Hash: factoryHash, SEP: 41, RulesetVersion: "sep41-v0.5.2", Status: "match", OKCount: 10,
		MissingJSON: "[]", MismatchedJSON: "[]", MatchedVariantsJSON: "{}", ComputedAt: "2026-10-26T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	var factoryIDs []string
	for i := 0; i < 2; i++ {
		raw := make([]byte, 32)
		raw[0] = byte(90 + i)
		id, err := strkey.Encode(strkey.VersionByteContract, raw)
		if err != nil {
			t.Fatal(err)
		}
		factoryIDs = append(factoryIDs, id)
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id, Kind: "wasm", CurrentWasmHash: factoryHash}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(factoryIDs)
	t.Setenv("SEP47IDX_DB", db)

	var out, stderr bytes.Buffer
	if code := run([]string{"gap", "--outreach", "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var report outreachOutput
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Tier != "inferred" || report.SEP != 41 || report.RulesetVersion != "sep41-v0.5.2" || report.TotalGroups != 2 || report.TotalContracts != 3 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Groups) != 2 || report.Groups[0].WasmHash != factoryHash || len(report.Groups[0].Contracts) != 2 || report.Groups[0].Contracts[0] != factoryIDs[0] {
		t.Fatalf("groups = %+v", report.Groups)
	}
	if report.Groups[0].ContractMeta != `contractmeta!(key = "sep", val = "41");` || !strings.Contains(report.Groups[0].PRTemplate, "Declared SEP-41") {
		t.Fatalf("group output = %+v", report.Groups[0])
	}
	if report.Groups[1].WasmHash != hash || len(report.Groups[1].Contracts) != 1 || report.Groups[1].Contracts[0] != ids[0] {
		t.Fatalf("second group = %+v", report.Groups[1])
	}

	out.Reset()
	stderr.Reset()
	if code := run([]string{"gap", "--outreach"}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, want := range []string{"**Inferred, not declared.**", "## Wasm `" + factoryHash + "` — 2 contract(s)", "- `" + factoryIDs[0] + "`", "contractmeta!(key = \"sep\", val = \"41\");", "Pull-request body"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("markdown lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "{{") {
		t.Errorf("unreplaced placeholder:\n%s", out.String())
	}
	t.Logf("outreach fixture output:\n%s", out.String())

	out.Reset()
	if code := run([]string{"gap", "--outreach", "--limit", "1", "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	report = outreachOutput{}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || len(report.Groups) != 1 || report.TotalGroups != 2 || report.TotalContracts != 3 {
		t.Fatalf("capped report = %+v, %v", report, err)
	}
	if code := run([]string{"gap", "--outreach", "--cursor", "abc", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
		t.Fatal("--outreach with --cursor accepted")
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
