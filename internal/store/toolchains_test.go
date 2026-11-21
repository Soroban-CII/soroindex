package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
)

func toolchainStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "tc.db"), Options{Passphrase: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	contract := 0
	add := func(hash, meta, status string, contracts int, declares bool, match string, archived bool) {
		if err := tx.UpsertWasm(ctx, WasmRow{Hash: hash, MetaJSON: meta, ParseStatus: status, ParserVersion: "1"}); err != nil {
			t.Fatal(err)
		}
		if declares {
			if err := tx.ReplaceClaims(ctx, hash, "sep47-meta", []ClaimRow{{SEP: 41, RawToken: "41"}}); err != nil {
				t.Fatal(err)
			}
		}
		if match != "" {
			if err := tx.UpsertMatch(ctx, MatchRow{Hash: hash, SEP: 41, RulesetVersion: "r1", Status: match, MissingJSON: "[]", MismatchedJSON: "[]", MatchedVariantsJSON: "{}", ComputedAt: "2026-11-21T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < contracts; i++ {
			contract++
			var b [32]byte
			b[0], b[1] = byte(contract>>8), byte(contract) // #nosec G115 -- fewer than 256 test contracts
			id, err := strkey.Encode(strkey.VersionByteContract, b[:])
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.UpsertContract(ctx, ContractRow{ID: id, Kind: "wasm", CurrentWasmHash: hash, Archived: archived}); err != nil {
				t.Fatal(err)
			}
		}
	}
	meta := func(rs, sdk, cli string) string {
		return fmt.Sprintf(`[{"key":"sep","value":"41"},{"key":"rsver","value":%q},{"key":"rssdkver","value":%q},{"key":"cliver","value":%q}]`, rs, sdk, cli)
	}
	h := func(n int) string { return fmt.Sprintf("%064x", n) }
	// A factory: one hash behind three contracts, declaring and matching.
	add(h(1), meta("1.98.1", "28.0.0#aa", "28.0.0#bb"), "ok", 3, true, "match", false)
	// Same toolchain, one contract, neither declaring nor matching.
	add(h(2), meta("1.98.1", "28.0.0#aa", "28.0.0#bb"), "ok", 1, false, "partial", false)
	// Older SDK, matches without declaring.
	add(h(3), meta("1.81.0", "21.7.6#cc", "21.0.0"), "ok", 1, false, "match", false)
	// No meta section at all.
	add(h(4), "", "ok", 1, false, "no_spec", false)
	// rsver repeated with two values; oversized SDK value; control character in CLI value.
	add(h(5), `[{"key":"rsver","value":"1.80.0"},{"key":"rsver","value":"1.81.0"},{"key":"rssdkver","value":"`+strings.Repeat("9", 65)+`"},{"key":"cliver","value":"22\u0000"}]`, "ok", 1, false, "", false)
	// Repeated identical values count once; empty value is unknown.
	add(h(6), `[{"key":"rsver","value":"1.98.1"},{"key":"rsver","value":"1.98.1"},{"key":"rssdkver","value":""}]`, "partial", 1, false, "", false)
	// Excluded: archived contract, and archived code.
	add(h(7), meta("0.0.1", "0.0.1", "0.0.1"), "ok", 2, true, "match", true)
	add(h(8), meta("0.0.2", "0.0.2", "0.0.2"), "archived", 1, true, "match", false)
	if err := tx.SetLastLedger(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return s
}

func bucket(t *testing.T, d ToolchainStats, key, version string) ToolchainBucket {
	t.Helper()
	for _, b := range d.Toolchains[key] {
		if b.Version == version {
			return b
		}
	}
	t.Fatalf("%s has no bucket %q: %+v", key, version, d.Toolchains[key])
	return ToolchainBucket{}
}

func TestToolchainsBucketsByHashAndContract(t *testing.T) {
	d, err := toolchainStore(t).Toolchains(context.Background(), "testnet", map[int]string{41: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if d.MeasuredHashes != 6 || d.MeasuredContracts != 8 || d.AsOfLedger != 42 || *d.SEP41RulesetVersion != "r1" {
		t.Fatalf("population: %+v", d)
	}
	got := bucket(t, d, "rssdkver", "28.0.0#aa")
	want := ToolchainBucket{Version: "28.0.0#aa", Hashes: 2, Contracts: 4, DeclaringHashes: 1, DeclaringContracts: 3, SEP41MatchHashes: 1, SEP41MatchContracts: 3,
		DeclaringPctByHash: 50, DeclaringPctByContract: 75, SEP41MatchPctByHash: 50, SEP41MatchPctByContract: 75}
	if got != want {
		t.Fatalf("factory weighting:\n got %+v\nwant %+v", got, want)
	}
	if b := bucket(t, d, "rssdkver", "21.7.6#cc"); b.Hashes != 1 || b.SEP41MatchHashes != 1 || b.DeclaringHashes != 0 || b.SEP41MatchPctByHash != 100 {
		t.Fatalf("older SDK: %+v", b)
	}
	// Missing meta, oversized value and empty value are all unknown.
	if b := bucket(t, d, "rssdkver", ToolchainUnknown); b.Hashes != 3 || b.Contracts != 3 {
		t.Fatalf("unknown SDK: %+v", b)
	}
	if b := bucket(t, d, "rsver", ToolchainMultiple); b.Hashes != 1 {
		t.Fatalf("multiple rsver: %+v", b)
	}
	if b := bucket(t, d, "rsver", "1.98.1"); b.Hashes != 3 || b.Contracts != 5 {
		t.Fatalf("repeated identical rsver counted wrongly: %+v", b)
	}
	if b := bucket(t, d, "cliver", ToolchainUnknown); b.Hashes != 3 {
		t.Fatalf("control character not unknown: %+v", b)
	}
	for _, k := range ToolchainKeys {
		hashes, contracts := 0, 0
		for _, b := range d.Toolchains[k] {
			hashes += b.Hashes
			contracts += b.Contracts
			if b.Version == "0.0.1" || b.Version == "0.0.2" {
				t.Fatalf("archived contract or code counted: %+v", b)
			}
		}
		if hashes != d.MeasuredHashes || contracts != d.MeasuredContracts {
			t.Fatalf("%s buckets do not sum to the population: %d/%d", k, hashes, contracts)
		}
	}
	if d.Toolchains["rsver"][0].Version != "1.98.1" {
		t.Fatalf("buckets not ordered by hashes: %+v", d.Toolchains["rsver"])
	}
}

func TestToolchainsWithoutSEP41Rules(t *testing.T) {
	d, err := toolchainStore(t).Toolchains(context.Background(), "testnet", map[int]string{})
	if err != nil {
		t.Fatal(err)
	}
	if d.SEP41RulesetVersion != nil {
		t.Fatal("ruleset reported without a rule file")
	}
	for _, b := range d.Toolchains["rsver"] {
		if b.SEP41MatchHashes != 0 {
			t.Fatalf("match counted without loaded rules: %+v", b)
		}
	}
}

func TestToolchainsRejectsCorruptStoredMeta(t *testing.T) {
	s := toolchainStore(t)
	if _, err := s.DB.Exec(`UPDATE wasm SET meta_json = '{' WHERE hash = ?`, fmt.Sprintf("%064x", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Toolchains(context.Background(), "testnet", nil); err == nil {
		t.Fatal("corrupt meta_json accepted")
	}
}
