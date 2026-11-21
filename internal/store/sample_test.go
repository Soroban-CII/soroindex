package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSampleContractsReproducible(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "sample.db"), Options{Passphrase: testnet})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		id, err := fixtureContractID(i)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.UpsertContract(ctx, ContractRow{ID: id, Kind: "wasm", CurrentWasmHash: fixtureHash(i), UpdatedLedger: uint32(10 + i)}); err != nil { // #nosec G115 -- test index
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	first, err := s.SampleContracts(ctx, 25, 7)
	if err != nil || len(first) != 25 {
		t.Fatalf("sample = %d rows, %v", len(first), err)
	}
	again, err := s.SampleContracts(ctx, 25, 7)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatalf("same seed changed the sample: %v", err)
	}
	other, err := s.SampleContracts(ctx, 25, 8)
	if err != nil || len(other) != 25 {
		t.Fatalf("second sample = %d rows, %v", len(other), err)
	}
	if reflect.DeepEqual(first, other) {
		t.Fatal("different seeds returned the same order")
	}
	seen := map[string]bool{}
	for _, c := range first {
		if seen[c.ID] {
			t.Fatalf("duplicate contract %s", c.ID)
		}
		seen[c.ID] = true
	}
	if _, err := s.SampleContracts(ctx, 0, 1); err == nil {
		t.Fatal("zero sample accepted")
	}
	if _, err := s.SampleContracts(ctx, 100001, 1); err == nil {
		t.Fatal("oversized sample accepted")
	}
}
