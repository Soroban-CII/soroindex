package ingest

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const testMainnetPassphrase = "Public Global Stellar Network ; September 2015"

// knownContractID is a valid C... strkey (32 zero bytes encoded in base32 with
// the contract version byte). Used across datalake tests as a fixture value.
const knownContractID = "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"

func TestDataLakeSourceRequiresPassphrase(t *testing.T) {
	src := &DataLakeSource{R: strings.NewReader(""), Passphrase: ""}
	_, err := src.Contracts(context.Background())
	if !errors.Is(err, ErrMissingPassphrase) {
		t.Fatalf("want ErrMissingPassphrase, got %v", err)
	}
}

func TestDataLakeSourceName(t *testing.T) {
	src := &DataLakeSource{Passphrase: testMainnetPassphrase, AsOfLedger: 12345}
	name := src.Name()
	if !strings.Contains(name, "hubble-export") {
		t.Fatalf("unexpected name: %q", name)
	}
	if !strings.Contains(name, "12345") {
		t.Fatalf("AsOfLedger not in name: %q", name)
	}
}

func TestDataLakeSourceEmptyFile(t *testing.T) {
	src := &DataLakeSource{R: strings.NewReader(""), Passphrase: testMainnetPassphrase}
	_, err := src.Contracts(context.Background())
	if err == nil {
		t.Fatal("expected error for empty file")
	}
}

func TestDataLakeSourceMissingIDColumn(t *testing.T) {
	csv := "wasm_hash\naabbcc\n"
	src := &DataLakeSource{R: strings.NewReader(csv), Passphrase: testMainnetPassphrase}
	_, err := src.Contracts(context.Background())
	if err == nil || !strings.Contains(err.Error(), "contract_id") {
		t.Fatalf("want missing contract_id column error, got %v", err)
	}
}

func TestDataLakeSourceHeaderOnly(t *testing.T) {
	csv := "contract_id,wasm_hash\n"
	src := &DataLakeSource{R: strings.NewReader(csv), Passphrase: testMainnetPassphrase}
	rows, err := src.Contracts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("want 0 rows, got %d", len(rows))
	}
}

func TestDataLakeSourceDeduplicates(t *testing.T) {
	hash := strings.Repeat("a", 64)
	csv := "contract_id,wasm_hash\n" +
		knownContractID + "," + hash + "\n" +
		knownContractID + "," + hash + "\n" // duplicate
	src := &DataLakeSource{R: strings.NewReader(csv), Passphrase: testMainnetPassphrase}
	rows, err := src.Contracts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row (dedup), got %d", len(rows))
	}
	if rows[0].ContractID != knownContractID {
		t.Fatalf("unexpected contract ID: %q", rows[0].ContractID)
	}
	if rows[0].WasmHash != hash {
		t.Fatalf("unexpected wasm hash: %q", rows[0].WasmHash)
	}
}

func TestDataLakeSourceExtraColumnsIgnored(t *testing.T) {
	csv := "extra1,contract_id,extra2,wasm_hash,extra3\n" +
		"ignore," + knownContractID + ",ignore," + strings.Repeat("b", 64) + ",ignore\n"
	src := &DataLakeSource{R: strings.NewReader(csv), Passphrase: testMainnetPassphrase}
	rows, err := src.Contracts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ContractID != knownContractID {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestDataLakeSourceBOMPrefix(t *testing.T) {
	// BOM at start of file (Excel CSV export convention)
	csv := "\xef\xbb\xbfcontract_id,wasm_hash\n" + knownContractID + ",\n"
	src := &DataLakeSource{R: strings.NewReader(csv), Passphrase: testMainnetPassphrase}
	rows, err := src.Contracts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d: %+v", len(rows), rows)
	}
}

func TestDataLakeSourceInvalidContractID(t *testing.T) {
	csv := "contract_id,wasm_hash\nnot-a-contract-id,\n"
	src := &DataLakeSource{R: strings.NewReader(csv), Passphrase: testMainnetPassphrase}
	_, err := src.Contracts(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid contract ID")
	}
}

func TestDataLakeSourceHashLowercased(t *testing.T) {
	upperHash := strings.Repeat("A", 64)
	csv := "contract_id,wasm_hash\n" + knownContractID + "," + upperHash + "\n"
	src := &DataLakeSource{R: strings.NewReader(csv), Passphrase: testMainnetPassphrase}
	rows, err := src.Contracts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].WasmHash != strings.ToLower(upperHash) {
		t.Fatalf("hash not lowercased: %q", rows[0].WasmHash)
	}
}

func TestDataLakeSourceIDColumnOnly(t *testing.T) {
	csv := "contract_id\n" + knownContractID + "\n"
	src := &DataLakeSource{R: strings.NewReader(csv), Passphrase: testMainnetPassphrase}
	rows, err := src.Contracts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ContractID != knownContractID || rows[0].WasmHash != "" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}
