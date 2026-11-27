package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/stellar/go-stellar-sdk/strkey"
)

// goldenHash is the SHA-256 of a committed fixture, which is the hash the
// indexer stores it under.
func goldenHash(t *testing.T, name string) string {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("../../testdata/wasm", name)) // #nosec G304 -- fixed fixture names
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(code)
	return hex.EncodeToString(sum[:])
}

// diffFixture indexes every golden fixture and adds one contract upgraded
// from token_full_sep.wasm to token_partial.wasm, so a test can take the two
// hashes out of `contract --history` exactly as an operator would.
func diffFixture(t *testing.T) (db, contractID, fullHash, partialHash string) {
	t.Helper()
	db = indexFixtures(t)
	fullHash, partialHash = goldenHash(t, "token_full_sep.wasm"), goldenHash(t, "token_partial.wasm")
	ctx := context.Background()
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var raw [32]byte
	raw[0] = 0x11
	contractID, err = strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.UpsertContract(ctx, store.ContractRow{ID: contractID, Kind: "wasm", CurrentWasmHash: partialHash}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []store.Version{{ContractID: contractID, WasmHash: fullHash, FromLedger: 10}, {ContractID: contractID, WasmHash: partialHash, FromLedger: 20}} {
		if _, err = tx.SetVersion(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.SetLastLedger(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db, contractID, fullHash, partialHash
}

func TestDiffHumanOutputGroupsChangesBySection(t *testing.T) {
	db, _, fullHash, partialHash := diffFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"diff", fullHash, partialHash}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	got := out.String()
	for _, want := range []string{
		fullHash + " -> " + partialHash + " (testnet)",
		"\nFunctions\n",
		"  - burn: (Address, i128) -> ()\n",
		"  - burn_from: (Address, Address, i128) -> ()\n",
		"\nVerdicts (inferred tier; not a claim and not a verification)\n",
		"  ~ SEP-41: match (sep41-v0.5.2, 10 functions matched, implements sep41-v0.5.2) -> partial (sep41-v0.5.2, 8 functions matched)\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "No differences") {
		t.Fatalf("an upgrade that drops two functions reported no differences:\n%s", got)
	}
}

func TestDiffReportsAddedFunctionsAndDeclarationChanges(t *testing.T) {
	db, _, fullHash, partialHash := diffFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	// The reverse direction: the same two fixtures, so burn and burn_from
	// must now be additions and the verdict must move the other way.
	var out, errb bytes.Buffer
	if code := run([]string{"diff", partialHash, fullHash}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	got := out.String()
	for _, want := range []string{
		"  + burn: (Address, i128) -> ()\n",
		"  + burn_from: (Address, Address, i128) -> ()\n",
		"  ~ SEP-41: partial (sep41-v0.5.2, 8 functions matched) -> match (sep41-v0.5.2, 10 functions matched, implements sep41-v0.5.2)\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}

	// token_full_legacy.wasm has the same ten functions with transfer.to as
	// Address and declares no SEP, so it exercises the declared and
	// changed-signature paths against token_full_sep.wasm.
	out.Reset()
	legacy := goldenHash(t, "token_full_legacy.wasm")
	if code := run([]string{"diff", fullHash, legacy}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	got = out.String()
	for _, want := range []string{
		"\nDeclared SEPs\n  - SEP-41 (sep47-meta) token=\"41\", anomaly=none\n",
		"  ~ transfer: was (Address, MuxedAddress, i128) -> (), now (Address, Address, i128) -> ()\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"  + burn", "  - burn", "  + transfer", "  - transfer"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("the legacy token has the same ten functions, so %q must not appear:\n%s", unwanted, got)
		}
	}
}

func TestDiffJSONIsDeterministicAndGroupsBySection(t *testing.T) {
	db, _, fullHash, partialHash := diffFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	var first, second, errb bytes.Buffer
	for _, out := range []*bytes.Buffer{&first, &second} {
		if code := run([]string{"diff", fullHash, partialHash, "--json"}, out, &errb); code != 0 {
			t.Fatal(code, errb.String())
		}
	}
	if first.String() != second.String() {
		t.Fatalf("two runs differ:\n%s\n---\n%s", first.String(), second.String())
	}
	var d diffOutput
	if err := json.Unmarshal(first.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.From.Hash != fullHash || d.To.Hash != partialHash || d.Identical {
		t.Fatalf("sides or identical wrong: %+v", d)
	}
	if !d.Functions.Comparable || len(d.Functions.Removed) != 2 || len(d.Functions.Added) != 0 {
		t.Fatalf("functions: %+v", d.Functions)
	}
	if d.Functions.Removed[0].Name != "burn" || d.Functions.Removed[1].Name != "burn_from" {
		t.Fatalf("removed functions are not sorted: %+v", d.Functions.Removed)
	}
	if len(d.Verdicts) != 1 || d.Verdicts[0].SEP != 41 || !d.Verdicts[0].Changed ||
		d.Verdicts[0].From.Status != "match" || d.Verdicts[0].To.Status != "partial" {
		t.Fatalf("verdicts: %+v", d.Verdicts)
	}
	if !d.Declared.empty() {
		t.Fatalf("both fixtures declare SEP-41 with the same token, so nothing declared changed: %+v", d.Declared)
	}
}

func TestDiffUsesHashesFromContractHistory(t *testing.T) {
	db, id, fullHash, partialHash := diffFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	var hist, errb bytes.Buffer
	if code := run([]string{"contract", id, "--history", "--json"}, &hist, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	var h store.HistoryDetail
	if err := json.Unmarshal(hist.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	var hashes []string
	for _, v := range h.History {
		if v.WasmHash != nil {
			hashes = append(hashes, *v.WasmHash)
		}
	}
	if len(hashes) != 2 {
		t.Fatalf("history gave %d hashes: %s", len(hashes), hist.String())
	}
	var out bytes.Buffer
	if code := run([]string{"diff", hashes[0], hashes[1], "--json"}, &out, &errb); code != 0 {
		t.Fatalf("diff of history hashes %v: %d %s", hashes, code, errb.String())
	}
	var d diffOutput
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.From.Hash+d.To.Hash != hashes[0]+hashes[1] {
		t.Fatalf("diff read other hashes than history gave: %+v", d)
	}
	if (hashes[0] == fullHash) == (hashes[1] == partialHash) && d.Identical {
		t.Fatalf("the upgrade changed the interface, so identical must be false: %+v", d)
	}
}

func TestDiffSameHashReportsNoDifferences(t *testing.T) {
	db, _, fullHash, _ := diffFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"diff", fullHash, fullHash}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	if !strings.Contains(out.String(), "No differences in metadata, declarations, functions or inferred verdicts.") {
		t.Fatalf("a hash against itself reported differences:\n%s", out.String())
	}
}

func TestDiffUnknownHashExitsNotFound(t *testing.T) {
	db, _, fullHash, _ := diffFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	unknown := strings.Repeat("b", 64)
	for _, args := range [][]string{{"diff", unknown, fullHash}, {"diff", fullHash, unknown}} {
		var errb bytes.Buffer
		if code := run(args, &bytes.Buffer{}, &errb); code != 2 {
			t.Fatalf("%v: exit %d, want 2 (%s)", args, code, errb.String())
		}
		if !strings.Contains(errb.String(), unknown) {
			t.Fatalf("%v: stderr does not name the missing hash: %s", args, errb.String())
		}
	}
}

func TestDiffRejectsBadInputWithExitOne(t *testing.T) {
	db, _, fullHash, partialHash := diffFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	for name, args := range map[string][]string{
		"one hash":        {"diff", fullHash},
		"three hashes":    {"diff", fullHash, partialHash, fullHash},
		"no hash":         {"diff"},
		"uppercase hash":  {"diff", strings.ToUpper(fullHash), partialHash},
		"not hex":         {"diff", fullHash, strings.Repeat("z", 64)},
		"short hash":      {"diff", fullHash, "abc"},
		"unknown flag":    {"diff", fullHash, partialHash, "--nope"},
		"flags then hash": {"diff", "--json", fullHash},
	} {
		t.Run(name+" exits 1", func(t *testing.T) {
			if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
				t.Fatalf("%v: exit %d, want 1", args, code)
			}
		})
	}
}

func TestDiffAcceptsFlagsBeforeHashes(t *testing.T) {
	db, _, fullHash, partialHash := diffFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"diff", "--json", fullHash, partialHash}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	var d diffOutput
	if err := json.Unmarshal(out.Bytes(), &d); err != nil || d.From.Hash != fullHash {
		t.Fatalf("%v, %s", err, out.String())
	}
}

// TestDiffReportsMissingFunctionRecordAsRefetch covers a Wasm stored before
// migration 0002: it has a spec, so its functions exist, but the index does
// not hold them. The diff must say so instead of reporting every function as
// removed, and must not claim the two are identical.
func TestDiffReportsMissingFunctionRecordAsRefetch(t *testing.T) {
	db, _, fullHash, partialHash := diffFixture(t)
	ctx := context.Background()
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE wasm SET functions_json = NULL WHERE hash = ?`, partialHash); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"diff", fullHash, partialHash}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	for _, want := range []string{"not compared", partialHash, "needs a re-fetch (analyzed before migration 0002)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
	out.Reset()
	if code := run([]string{"diff", fullHash, partialHash, "--json"}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	var d diffOutput
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Functions.Comparable || len(d.Functions.Removed) != 0 || !d.To.NeedsRefetch || d.From.NeedsRefetch {
		t.Fatalf("functions %+v, sides %+v / %+v", d.Functions, d.From, d.To)
	}
	if d.Identical {
		t.Fatal("identical must be false when the interface could not be compared")
	}
}

// TestDiffNoSpecIsNotARefetch separates the two reasons signatures can be
// absent: no_meta.wasm is a real fixture with no spec section, so it has no
// functions to store and nothing to re-fetch.
func TestDiffNoSpecIsNotARefetch(t *testing.T) {
	db, _, fullHash, _ := diffFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	noMeta := goldenHash(t, "no_meta.wasm")
	var out, errb bytes.Buffer
	if code := run([]string{"diff", fullHash, noMeta, "--json"}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	var d diffOutput
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.To.NeedsRefetch {
		t.Fatalf("a fixture with a spec section reported needs_refetch: %+v", d.To)
	}
	if !d.Functions.Comparable {
		t.Fatalf("both sides have stored functions, so the interface is comparable: %+v", d.Functions)
	}
}

// TestDiffMetaDecodeErrorIsReported proves malformed stored metadata becomes
// an error with the hash in it, not a panic and not a silent empty section.
func TestDiffMetaDecodeErrorIsReported(t *testing.T) {
	db, _, fullHash, partialHash := diffFixture(t)
	ctx := context.Background()
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE wasm SET meta_json = '{"key":"sep"}' WHERE hash = ?`, partialHash); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEP47IDX_DB", db)
	var errb bytes.Buffer
	if code := run([]string{"diff", fullHash, partialHash}, &bytes.Buffer{}, &errb); code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "stored metadata of "+partialHash) {
		t.Fatalf("stderr does not name the bad row: %s", errb.String())
	}
}

func TestDiffIsListedInUsage(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"help"}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), "diff         compare two stored Wasm") {
		t.Fatalf("diff is missing from the command list:\n%s", out.String())
	}
}
