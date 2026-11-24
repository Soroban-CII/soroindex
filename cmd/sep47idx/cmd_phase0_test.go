package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/phase0"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/stellar/go-stellar-sdk/strkey"
)

// TestPhase0RecordToolchainsFromFixtureIndex records the toolchain census
// from a seeded fixture index and checks the generated ADOPTION.md rows.
func TestPhase0RecordToolchainsFromFixtureIndex(t *testing.T) {
	db, _, hash := readFixture(t)
	ctx := context.Background()
	st, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	withMeta := strings.Repeat("b", 64)
	noMeta := strings.Repeat("c", 64)
	for _, w := range []store.WasmRow{
		{Hash: withMeta, MetaJSON: `[{"key":"rsver","value":"1.98.1"},{"key":"rssdkver","value":"28.0.0#aa"}]`, ParseStatus: "ok", ParserVersion: "1"},
		{Hash: noMeta, ParseStatus: "ok", ParserVersion: "1"},
	} {
		if err := tx.UpsertWasm(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	for i, h := range []string{hash, withMeta, noMeta} {
		raw := make([]byte, 32)
		raw[0] = byte(i + 20)
		id, err := strkey.Encode(strkey.VersionByteContract, raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id, Kind: "wasm", CurrentWasmHash: h}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	s := phase0.Summary{
		Network: "testnet", GeneratedAt: "2026-10-07T00:00:00Z", LatestLedger: 10,
		ParserVersion: "1", RulesetVersions: []string{"sep41-v0.5.2"},
		Method: "fixture", Limits: sepmeta.DefaultLimits(),
	}
	s.Contracts.Measured, s.Hashes.Measured = 4, 3
	if err := phase0.WriteSummary(dir, s); err != nil {
		t.Fatal(err)
	}
	if md, err := os.ReadFile(filepath.Join(dir, "ADOPTION.md")); err != nil || !strings.Contains(string(md), "Not recorded for this run") { // #nosec G304 -- test-owned temp directory
		t.Fatalf("summary without a census should say it is not recorded: %v", err)
	}

	t.Setenv("SEP47IDX_NETWORK", "testnet")
	t.Setenv("SEP47IDX_DB", db)
	var out, stderr bytes.Buffer
	if code := run([]string{"phase0", "--network", "testnet", "--db", db, "--out", dir, "--record-toolchains", db}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(out.String(), "3 hashes / 4 contracts carry toolchain meta as of ledger 10") {
		t.Fatalf("stdout = %q", out.String())
	}
	md, err := os.ReadFile(filepath.Join(dir, "ADOPTION.md")) // #nosec G304 -- test-owned temp directory
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"### Toolchain versions",
		"Measured: 3 hashes / 4 contracts, as of ledger 10",
		"| `1.98.1` | 1 | 1 | 0 / 1 (0.00%) / 0 / 1 (0.00%) |",
		"| `unknown` | 2 | 3 | 1 / 2 (50.00%) / 2 / 3 (66.67%) |",
		"| `28.0.0#aa` | 1 | 1 |",
	} {
		if !strings.Contains(string(md), want) {
			t.Errorf("ADOPTION.md lacks %q\n%s", want, md)
		}
	}
	out.Reset()
	stderr.Reset()
	if code := run([]string{"phase0", "--network", "testnet", "--db", db, "--out", dir, "--record-toolchains", db}, &out, &stderr); code != 1 || !strings.Contains(stderr.String(), "already records") {
		t.Fatalf("second record run should fail: exit %d, %q", code, stderr.String())
	}
}
