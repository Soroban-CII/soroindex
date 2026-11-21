package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Soroban-CII/soroindex/internal/claims"
	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
	"github.com/stellar/go-stellar-sdk/xdr"
)

var inspectFixtures = []string{"token_full_sep.wasm", "token_full_legacy.wasm", "token_partial.wasm", "not_token.wasm", "multi_sep.wasm", "no_meta.wasm", "truncated.wasm"}

// indexFixtures writes each golden fixture as a created CONTRACT_CODE entry
// through the indexer's ledger path, as sync would, and returns the database.
func indexFixtures(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "testnet.db")
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rf, err := match.LoadRules(rules.FS)
	if err != nil {
		t.Fatal(err)
	}
	ix := &ingest.Indexer{Store: s, Rules: rf, Limits: sepmeta.DefaultLimits(), Claims: claims.Default(sepmeta.DefaultLimits()),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now}
	facts := ingest.ContractFacts{Ledger: 10}
	for _, name := range inspectFixtures {
		code, err := os.ReadFile(filepath.Join("../../testdata/wasm", name)) // #nosec G304 -- fixed fixture names
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(code)
		entry := xdr.LedgerEntry{Data: xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.ContractCodeEntry{Hash: xdr.Hash(sum), Code: code}}}
		facts.Changes = append(facts.Changes, ingest.Change{Kind: ingest.Created, Entry: &entry})
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.ApplyLedger(ctx, tx, facts); err != nil {
		t.Fatal(err)
	}
	if err := tx.SetLastLedger(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestInspectOutputMatchesIndexedWasm(t *testing.T) {
	t.Setenv("SEP47IDX_DB", indexFixtures(t))
	for _, name := range inspectFixtures {
		t.Run(name+" matches wasm output", func(t *testing.T) {
			path := filepath.Join("../../testdata/wasm", name)
			code, err := os.ReadFile(path) // #nosec G304 -- fixed fixture names
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(code)
			hash := hex.EncodeToString(sum[:])

			var indexed, local, errb bytes.Buffer
			if c := run([]string{"wasm", hash, "--json"}, &indexed, &errb); c != 0 {
				t.Fatal(c, errb.String())
			}
			if c := run([]string{"inspect", path, "--json"}, &local, &errb); c != 0 {
				t.Fatal(c, errb.String())
			}
			var d store.WasmDetail
			var e ingest.Evidence
			if err := json.Unmarshal(indexed.Bytes(), &d); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(local.Bytes(), &e); err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(ingest.Evidence{Hash: d.Hash, Meta: d.Meta, Claims: d.Claims, Anomalies: d.Anomalies, Matches: d.Matches,
				ParseStatus: d.ParseStatus, ParseError: d.ParseError, ParserVersion: d.ParserVersion})
			got, _ := json.Marshal(e)
			if !bytes.Equal(compactJSON(t, got), compactJSON(t, want)) {
				t.Fatalf("JSON drift\ninspect: %s\n   wasm: %s", got, want)
			}

			indexed.Reset()
			local.Reset()
			if c := run([]string{"wasm", hash}, &indexed, &errb); c != 0 {
				t.Fatal(c, errb.String())
			}
			if c := run([]string{"inspect", path}, &local, &errb); c != 0 {
				t.Fatal(c, errb.String())
			}
			wasmBody := strings.SplitN(indexed.String(), "\n", 2)[1]
			wasmBody = wasmBody[:strings.Index(wasmBody, "Live contracts on this page:")]
			inspectBody := strings.SplitN(local.String(), "\n", 2)[1]
			if wasmBody != inspectBody {
				t.Fatalf("human output drift\ninspect:\n%s\nwasm:\n%s", inspectBody, wasmBody)
			}
		})
	}
}

func compactJSON(t *testing.T, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestInspectExitCodes(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.wasm")
	if err := os.WriteFile(big, make([]byte, sepmeta.DefaultLimits().MaxWasmBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(dir, "junk.wasm")
	if err := os.WriteFile(junk, []byte("not wasm"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := func(n string) string { return filepath.Join("../../testdata/wasm", n) }
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"full token analysed", []string{fixture("token_full_sep.wasm")}, 0},
		{"full token passes required match", []string{fixture("token_full_sep.wasm"), "--require-match", "41"}, 0},
		{"partial token analysed without a gate", []string{fixture("token_partial.wasm")}, 0},
		{"partial token fails required match", []string{fixture("token_partial.wasm"), "--require-match", "41"}, 1},
		{"false claim fails required match", []string{fixture("not_token.wasm"), "--require-match", "41"}, 1},
		{"malformed Wasm is analysed and recorded", []string{junk}, 0},
		{"malformed Wasm fails required match", []string{junk, "--require-match", "41"}, 1},
		{"required SEP without a rule file is an error", []string{fixture("token_full_sep.wasm"), "--require-match", "9999"}, 1},
		{"negative SEP is an error", []string{fixture("token_full_sep.wasm"), "--require-match", "-1"}, 1},
		{"missing file is an error", []string{filepath.Join(dir, "absent.wasm")}, 1},
		{"file over the Wasm limit is an error", []string{big}, 1},
		{"no file is an error", []string{}, 1},
		{"two files are an error", []string{junk, junk}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if got := run(append([]string{"inspect"}, tt.args...), &out, &errb); got != tt.want {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", got, tt.want, out.String(), errb.String())
			}
		})
	}
}

// TestInspectNeedsNoDatabase points the database at a path that cannot be
// opened; inspect must not try.
func TestInspectNeedsNoDatabase(t *testing.T) {
	t.Setenv("SEP47IDX_DB", filepath.Join(t.TempDir(), "missing", "dir", "x.db"))
	t.Setenv("SEP47IDX_RPC_URL", "http://127.0.0.1:1")
	var out, errb bytes.Buffer
	if c := run([]string{"inspect", "../../testdata/wasm/multi_sep.wasm"}, &out, &errb); c != 0 {
		t.Fatal(c, errb.String())
	}
	for _, want := range []string{"Declared SEP-40 (sep47-meta)", "Declared SEP-41 (sep47-meta)", "local file ../../testdata/wasm/multi_sep.wasm, not indexed"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in\n%s", want, out.String())
		}
	}
}
