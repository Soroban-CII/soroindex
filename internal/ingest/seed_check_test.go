package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// loadFixture reads one golden Wasm and returns its bytes and hash.
func loadFixture(t *testing.T, name string) ([]byte, string) {
	t.Helper()
	b, err := fs.ReadFile(os.DirFS("../../testdata/wasm"), name)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:])
}

// checkIndexer builds an Indexer suitable for CheckSeed tests.
func checkIndexer(t *testing.T, r RPC) *Indexer {
	t.Helper()
	rf, err := match.LoadRules(rules.FS)
	if err != nil {
		t.Fatal(err)
	}
	return &Indexer{
		RPC:     r,
		Rules:   rf,
		Limits:  sepmeta.DefaultLimits(),
		Log:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Now:     func() time.Time { return time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) },
	}
}

func TestCheckSeed_ParseErrors(t *testing.T) {
	t.Run("empty file", func(t *testing.T) {
		ix := checkIndexer(t, nil)
		_, err := ix.CheckSeed(context.Background(), strings.NewReader(""))
		if err == nil {
			t.Fatal("want error for empty file")
		}
	})

	t.Run("missing header column", func(t *testing.T) {
		ix := checkIndexer(t, nil)
		_, err := ix.CheckSeed(context.Background(), strings.NewReader("other_col\nval\n"))
		if err == nil {
			t.Fatal("want error for missing contract_id column")
		}
	})
}

func TestCheckSeed_InvalidIDs(t *testing.T) {
	csv := "contract_id\nNOT-A-CONTRACT-ID\n" + seedCID(1) + "\n"
	ix := checkIndexer(t, nil)
	res, err := ix.CheckSeed(context.Background(), strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Invalid) == 0 {
		t.Fatal("want invalid IDs reported")
	}
	if res.Rows != 1 {
		t.Fatalf("rows = %d, want 1 (valid)", res.Rows)
	}
	// Network should not be hit when invalids are present.
	if res.LatestLedger != 0 {
		t.Fatalf("LatestLedger = %d, want 0 (no network calls for invalid file)", res.LatestLedger)
	}
}

func TestCheckSeed_Duplicates(t *testing.T) {
	id := seedCID(1)
	csv := "contract_id\n" + id + "\n" + id + "\n" + id + "\n"

	var called int
	fake := &entryRPC{t: t, latest: 100,
		data: map[string]xdr.ContractDataEntry{},
		code: map[string][]byte{},
	}
	ix := checkIndexer(t, fake)
	_ = called
	res, err := ix.CheckSeed(context.Background(), strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows != 1 {
		t.Errorf("rows = %d, want 1 (after dedup)", res.Rows)
	}
	if res.Duplicates != 2 {
		t.Errorf("duplicates = %d, want 2", res.Duplicates)
	}
	if res.NotFound != 1 {
		t.Errorf("not_found = %d, want 1 (id not on RPC)", res.NotFound)
	}
}

func TestCheckSeed_MatchesSeedTotals(t *testing.T) {
	// Build a fixture seed with wasm, wasm_ref, SAC and not-found contracts.
	wasmCode, wasmHash := loadFixture(t, "token_full_sep.wasm")

	owner := seedCID(50)
	refTag := "v1"

	idWasm := seedCID(1)
	idRef := seedCID(2)
	idSAC := seedCID(3)
	idMissing := seedCID(4)
	idArchived := seedCID(5)
	idHintMismatch := seedCID(6)

	wrongHash := strings.Repeat("ab", 32)

	wasmInstKey, _ := InstanceKey(idWasm)
	refInstKey, _ := InstanceKey(idRef)
	sacInstKey, _ := InstanceKey(idSAC)
	archivedInstKey, _ := InstanceKey(idArchived)
	hintInstKey, _ := InstanceKey(idHintMismatch)
	codeKey := hex.EncodeToString([]byte(wasmHash)) // just for the map

	// Build instance entries.
	mkWasm := func(id, hash string) xdr.ContractDataEntry {
		return xdr.ContractDataEntry{
			Contract:   seedAddr(id),
			Durability: xdr.ContractDataDurabilityPersistent,
			Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
			Val:        xdr.ScVal{Type: xdr.ScValTypeScvContractInstance, Instance: &xdr.ScContractInstance{Executable: seedWasmExec(hash)}},
		}
	}
	mkRef := func(id, ownerID, tag string) xdr.ContractDataEntry {
		return xdr.ContractDataEntry{
			Contract:   seedAddr(id),
			Durability: xdr.ContractDataDurabilityPersistent,
			Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
			Val:        xdr.ScVal{Type: xdr.ScValTypeScvContractInstance, Instance: &xdr.ScContractInstance{Executable: seedRefExec(ownerID, tag)}},
		}
	}
	mkSAC := func(id string) xdr.ContractDataEntry {
		sacExec := xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableStellarAsset}
		return xdr.ContractDataEntry{
			Contract:   seedAddr(id),
			Durability: xdr.ContractDataDurabilityPersistent,
			Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
			Val:        xdr.ScVal{Type: xdr.ScValTypeScvContractInstance, Instance: &xdr.ScContractInstance{Executable: sacExec}},
		}
	}
	_ = codeKey
	_ = wasmCode

	live := uint32(200)
	fake := &entryRPC{
		t:      t,
		latest: 100,
		code:   map[string][]byte{wasmHash: wasmCode},
		data: map[string]xdr.ContractDataEntry{
			wasmInstKey:    mkWasm(idWasm, wasmHash),
			refInstKey:     mkRef(idRef, owner, refTag),
			sacInstKey:     mkSAC(idSAC),
			archivedInstKey: mkWasm(idArchived, wasmHash), // will be expired
			hintInstKey:    mkWasm(idHintMismatch, wasmHash),
		},
		expired: map[string]bool{archivedInstKey: true},
	}
	_ = live

	// Build a seed CSV with a hint mismatch.
	csv := "contract_id,wasm_hash\n" +
		idWasm + "," + wasmHash + "\n" +
		idRef + ",\n" +
		idSAC + ",\n" +
		idMissing + ",\n" +
		idArchived + ",\n" +
		idHintMismatch + "," + wrongHash + "\n"

	ix := checkIndexer(t, fake)
	res, err := ix.CheckSeed(context.Background(), strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Invalid) != 0 {
		t.Errorf("invalid = %v, want none", res.Invalid)
	}
	if res.Rows != 6 {
		t.Errorf("rows = %d, want 6", res.Rows)
	}
	if res.Duplicates != 0 {
		t.Errorf("duplicates = %d, want 0", res.Duplicates)
	}
	if res.Found != 3 { // wasm + ref + sac
		t.Errorf("found = %d, want 3", res.Found)
	}
	if res.NotFound != 1 {
		t.Errorf("not_found = %d, want 1", res.NotFound)
	}
	if res.Archived != 1 {
		t.Errorf("archived = %d, want 1", res.Archived)
	}
	if res.HintMismatch != 1 {
		t.Errorf("hint_mismatch = %d, want 1", res.HintMismatch)
	}
	if res.Wasm != 1 {
		t.Errorf("wasm = %d, want 1", res.Wasm)
	}
	if res.WasmRef != 1 {
		t.Errorf("wasm_ref = %d, want 1", res.WasmRef)
	}
	if res.SAC != 1 {
		t.Errorf("sac = %d, want 1", res.SAC)
	}
	if res.LatestLedger != 100 {
		t.Errorf("latest_ledger = %d, want 100", res.LatestLedger)
	}
}

// seedAddr builds an xdr.ScAddress from a contract ID string.
func seedAddr(id string) xdr.ScAddress {
	raw, _ := strkey.Decode(strkey.VersionByteContract, id)
	var c xdr.ContractId
	copy(c[:], raw)
	return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &c}
}

// seedWasmExec builds a wasm executable from a hex hash string.
func seedWasmExec(hash string) xdr.ContractExecutable {
	var h xdr.Hash
	b, _ := hex.DecodeString(hash)
	copy(h[:], b)
	return xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &h}
}

// seedRefExec builds an external-ref executable.
func seedRefExec(ownerID, tag string) xdr.ContractExecutable {
	raw, _ := strkey.Decode(strkey.VersionByteContract, ownerID)
	var c xdr.ContractId
	copy(c[:], raw)
	owner := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &c}
	return xdr.ContractExecutable{
		Type:        xdr.ContractExecutableTypeContractExecutableExternalRef,
		ExternalRef: &xdr.ContractExecutableExternalRef{ExecutableOwner: owner, Tag: xdr.ScString(tag)},
	}
}
