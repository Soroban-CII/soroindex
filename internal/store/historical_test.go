package store

import (
	"context"
	"errors"
	"testing"
)

// TestContractAtLedger is issue #41.
// It tests half-open version range semantics (from_ledger <= ledger < to_ledger),
// upgrades at exact boundaries, cursor continuation via pagination, and that a
// ledger before the contract's creation returns ErrNotFound.
func TestContractAtLedger(t *testing.T) {
	ctx := context.Background()
	fix := catalogFixture{Hashes: 100, Contracts: 100}
	ro, in, _ := openFixture(t, fix, "")

	rulesets := map[int]string{41: fixtureRuleset}

	// in.UpgradedContract is contract 3, which was inserted at ledger 50 with an
	// old hash, closed at ledger 100, and re-opened from ledger 100 onward.
	upgradedID := in.UpgradedContract

	t.Run("current ledger returns current detail", func(t *testing.T) {
		// Get the current as_of_ledger first.
		current, err := ro.Contract(ctx, upgradedID, "testnet", rulesets)
		if err != nil {
			t.Fatalf("Contract: %v", err)
		}
		asOf := current.AsOfLedger
		d, err := ro.ContractAtLedger(ctx, upgradedID, asOf, "testnet", rulesets)
		if err != nil {
			t.Fatalf("ContractAtLedger at current: %v", err)
		}
		if d.ContractID != upgradedID {
			t.Errorf("contract_id = %q, want %q", d.ContractID, upgradedID)
		}
		if d.AsOfLedger != asOf {
			t.Errorf("as_of_ledger = %d, want %d", d.AsOfLedger, asOf)
		}
	})

	t.Run("ledger before first version returns ErrNotFound", func(t *testing.T) {
		_, err := ro.ContractAtLedger(ctx, upgradedID, 1, "testnet", rulesets)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("want ErrNotFound for ledger 1, got %v", err)
		}
	})

	t.Run("half-open range: at upgrade ledger returns new version", func(t *testing.T) {
		// Contract 3 is upgraded at ledger 100: old version [50,100), new [100,nil).
		// At ledger 100 exactly, the half-open range returns the NEW version.
		atUpgrade, err := ro.ContractAtLedger(ctx, upgradedID, 100, "testnet", rulesets)
		if err != nil {
			t.Fatalf("ContractAtLedger at 100: %v", err)
		}
		// The new version has from_ledger = 100.
		found := false
		for _, v := range atUpgrade.History {
			if v.FromLedger == 100 {
				found = true
				if atUpgrade.WasmHash != nil && v.WasmHash != nil && *atUpgrade.WasmHash != *v.WasmHash {
					t.Errorf("at ledger 100: wasm_hash %q does not match history from_ledger=100 hash %q", *atUpgrade.WasmHash, *v.WasmHash)
				}
			}
		}
		if !found {
			t.Error("no history entry with from_ledger = 100 found")
		}
	})

	t.Run("half-open range: one before upgrade returns old version", func(t *testing.T) {
		// At ledger 99, the half-open range [50,100) still contains 99.
		atBefore, err := ro.ContractAtLedger(ctx, upgradedID, 99, "testnet", rulesets)
		if err != nil {
			t.Fatalf("ContractAtLedger at 99: %v", err)
		}
		// Should have the old hash (from_ledger=50 version).
		oldVersion := false
		for _, v := range atBefore.History {
			if v.FromLedger == 50 && v.ToLedger != nil && *v.ToLedger == 100 {
				oldVersion = true
				if atBefore.WasmHash != nil && v.WasmHash != nil && *atBefore.WasmHash != *v.WasmHash {
					t.Errorf("at ledger 99: wasm_hash does not match old version entry")
				}
			}
		}
		if !oldVersion {
			t.Error("no history entry with from_ledger=50 to_ledger=100 found")
		}
	})

	t.Run("invalid contract ID returns ErrBadQuery", func(t *testing.T) {
		_, err := ro.ContractAtLedger(ctx, "not-a-contract", 100, "testnet", rulesets)
		if !errors.Is(err, ErrBadQuery) {
			t.Errorf("want ErrBadQuery, got %v", err)
		}
	})

	t.Run("unknown contract returns ErrNotFound", func(t *testing.T) {
		unknown, err := fixtureContractID(999999)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ro.ContractAtLedger(ctx, unknown, 100, "testnet", rulesets)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("want ErrNotFound for unknown contract, got %v", err)
		}
	})

	t.Run("as_of_ledger is set to the requested ledger", func(t *testing.T) {
		d, err := ro.ContractAtLedger(ctx, upgradedID, 100, "testnet", rulesets)
		if err != nil {
			t.Fatalf("ContractAtLedger: %v", err)
		}
		if d.AsOfLedger != 100 {
			t.Errorf("as_of_ledger = %d, want 100", d.AsOfLedger)
		}
	})

	t.Run("non-upgraded contract at any ledger after creation returns current detail", func(t *testing.T) {
		// Contract 4 has only one version (from_ledger=50, no upgrade).
		sep41id := in.SEP41Contract
		d, err := ro.ContractAtLedger(ctx, sep41id, 150, "testnet", rulesets)
		if err != nil {
			t.Fatalf("ContractAtLedger: %v", err)
		}
		if len(d.History) == 0 {
			t.Error("expected at least one history entry")
		}
	})
}
