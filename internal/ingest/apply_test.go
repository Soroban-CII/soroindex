package ingest

import (
	"context"
	"errors"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

func instanceChange(t *testing.T, id, hash string, kind ChangeKind) Change {
	cd := instanceEntry(t, execWasm(hash), nil)
	cd.Contract = contractAddr(t, id)
	return dataChange(cd, kind)
}
func dataChange(cd xdr.ContractDataEntry, kind ChangeKind) Change {
	return Change{Kind: kind, Entry: &xdr.LedgerEntry{Data: xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractData, ContractData: &cd}}}
}
func refChange(t *testing.T, owner, tag, hash string) Change {
	h, err := ParseWasmHash(hash)
	if err != nil {
		t.Fatal(err)
	}
	b := xdr.ScBytes(h[:])
	s := xdr.ScString(tag)
	return dataChange(xdr.ContractDataEntry{Contract: contractAddr(t, owner), Durability: xdr.ContractDataDurabilityPersistent, Key: xdr.ScVal{Type: xdr.ScValTypeScvExecutableTag, ExecutableTag: &s}, Val: xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &b}}, Updated)
}
func referenceInstance(t *testing.T, id, owner, tag string) Change {
	cd := instanceEntry(t, xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableExternalRef, ExternalRef: &xdr.ContractExecutableExternalRef{ExecutableOwner: contractAddr(t, owner), Tag: xdr.ScString(tag)}}, nil)
	cd.Contract = contractAddr(t, id)
	return dataChange(cd, Created)
}
func applyFacts(t *testing.T, ix *Indexer, facts ...ContractFacts) {
	t.Helper()
	ctx := context.Background()
	tx, err := ix.Store.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, f := range facts {
		if err := ix.ApplyLedger(ctx, tx, f); err != nil {
			t.Fatal(err)
		}
	}
	if len(facts) > 0 {
		if err := tx.SetLastLedger(ctx, facts[len(facts)-1].Ledger); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func TestLedgerReplayIdenticalDatabase(t *testing.T) {
	code, h := wasmFixture(t, "token_full_sep.wasm")
	partial, h2 := wasmFixture(t, "token_partial.wasm")
	ix, s := newIndexer(t, &entryRPC{t: t, latest: 20, code: map[string][]byte{h: code, h2: partial}})
	facts := []ContractFacts{{Ledger: 10, Changes: []Change{instanceChange(t, seedCID(1), h, Created)}}, {Ledger: 20, Changes: []Change{instanceChange(t, seedCID(1), h2, Updated)}}}
	applyFacts(t, ix, facts...)
	before := dump(t, s)
	applyFacts(t, ix, facts...)
	if after := dump(t, s); before != after {
		t.Fatalf("replay changed tables:\nbefore %s\nafter %s", before, after)
	}
}
func TestInstanceUpgradeExactlyOneVersion(t *testing.T) {
	code, h := wasmFixture(t, "token_full_sep.wasm")
	partial, h2 := wasmFixture(t, "token_partial.wasm")
	ix, s := newIndexer(t, &entryRPC{t: t, latest: 20, code: map[string][]byte{h: code, h2: partial}})
	applyFacts(t, ix, ContractFacts{Ledger: 10, Changes: []Change{instanceChange(t, seedCID(1), h, Created)}}, ContractFacts{Ledger: 20, Changes: []Change{instanceChange(t, seedCID(1), h2, Updated)}})
	var count, closed int
	if err := s.DB.QueryRow(`SELECT count(*),sum(to_ledger = 20) FROM contract_versions`).Scan(&count, &closed); err != nil || count != 2 || closed != 1 {
		t.Fatalf("versions %d closed %d: %v", count, closed, err)
	}
	var status string
	if err := s.DB.QueryRow(`SELECT status FROM interface_matches WHERE wasm_hash = ?`, h2).Scan(&status); err != nil || status != "partial" {
		t.Fatalf("upgrade status %q: %v", status, err)
	}
}
func TestArchivedCodeKeepsClaimsAndRestores(t *testing.T) {
	code, h := wasmFixture(t, "token_full_sep.wasm")
	ix, s := newIndexer(t, &entryRPC{t: t, latest: 20, code: map[string][]byte{h: code}})
	applyFacts(t, ix, ContractFacts{Ledger: 10, Changes: []Change{instanceChange(t, seedCID(1), h, Created)}})
	hash, _ := ParseWasmHash(h)
	applyFacts(t, ix, ContractFacts{Ledger: 20, Changes: []Change{{Kind: Evicted, Key: &xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.LedgerKeyContractCode{Hash: hash}}}}})
	var claims, archived int
	if err := s.DB.QueryRow(`SELECT count(*) FROM wasm_claims WHERE wasm_hash = ?`, h).Scan(&claims); err != nil || claims != 1 {
		t.Fatalf("claims %d: %v", claims, err)
	}
	if err := s.DB.QueryRow(`SELECT archived FROM contracts`).Scan(&archived); err != nil || archived != 1 {
		t.Fatalf("archived %d: %v", archived, err)
	}
	applyFacts(t, ix, ContractFacts{Ledger: 21, Changes: []Change{{Kind: Restored, Entry: &xdr.LedgerEntry{Data: xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.ContractCodeEntry{Hash: hash, Code: code}}}}}})
	if err := s.DB.QueryRow(`SELECT archived FROM contracts`).Scan(&archived); err != nil || archived != 0 {
		t.Fatalf("restored %d: %v", archived, err)
	}
}
func TestReferenceUpgradeOnlyReferencingContracts(t *testing.T) {
	code, h := wasmFixture(t, "token_full_sep.wasm")
	partial, h2 := wasmFixture(t, "token_partial.wasm")
	ix, s := newIndexer(t, &entryRPC{t: t, latest: 20, code: map[string][]byte{h: code, h2: partial}})
	owner := seedCID(9)
	applyFacts(t, ix, ContractFacts{Ledger: 10, Changes: []Change{refChange(t, owner, "v1", h), referenceInstance(t, seedCID(1), owner, "v1"), referenceInstance(t, seedCID(2), owner, "v1"), instanceChange(t, seedCID(3), h, Created)}})
	applyFacts(t, ix, ContractFacts{Ledger: 20, Changes: []Change{refChange(t, owner, "v1", h2)}})
	var changed, versions int
	if err := s.DB.QueryRow(`SELECT count(*) FROM contracts WHERE current_wasm_hash = ?`, h2).Scan(&changed); err != nil || changed != 2 {
		t.Fatalf("changed %d: %v", changed, err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM contract_versions WHERE from_ledger = 20`).Scan(&versions); err != nil || versions != 2 {
		t.Fatalf("versions %d: %v", versions, err)
	}
	before := dump(t, s)
	applyFacts(t, ix, ContractFacts{Ledger: 20, Changes: []Change{refChange(t, owner, "v1", h2)}})
	if dump(t, s) != before {
		t.Fatal("reference replay changed database")
	}
}
func TestUnresolvedReferenceClaimsNothing(t *testing.T) {
	ix, s := newIndexer(t, &entryRPC{t: t, latest: 20})
	applyFacts(t, ix, ContractFacts{Ledger: 10, Changes: []Change{referenceInstance(t, seedCID(1), seedCID(9), "missing")}})
	var hash *string
	var count int
	if err := s.DB.QueryRow(`SELECT current_wasm_hash FROM contracts`).Scan(&hash); err != nil || hash != nil {
		t.Fatalf("hash %v: %v", hash, err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM claims WHERE current = 1`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("claims %d: %v", count, err)
	}
}
func TestMalformedFactsRollBack(t *testing.T) {
	ix, _ := newIndexer(t, &entryRPC{t: t})
	tx, err := ix.Store.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ix.ApplyLedger(context.Background(), tx, ContractFacts{Ledger: 10, Skipped: []error{ErrMalformed}}); !errors.Is(err, ErrMalformed) {
		t.Fatal(err)
	}
}

func TestReferenceArchivalClaimsNothingUntilRestored(t *testing.T) {
	code, h := wasmFixture(t, "token_full_sep.wasm")
	ix, s := newIndexer(t, &entryRPC{t: t, latest: 30, code: map[string][]byte{h: code}})
	owner, id := seedCID(9), seedCID(1)
	initial := ContractFacts{Ledger: 10, Changes: []Change{refChange(t, owner, "v1", h), referenceInstance(t, id, owner, "v1")}}
	applyFacts(t, ix, initial)
	keyText, err := ExecRefKey(owner, "v1")
	if err != nil {
		t.Fatal(err)
	}
	var key xdr.LedgerKey
	if err := xdr.SafeUnmarshalBase64(keyText, &key); err != nil {
		t.Fatal(err)
	}
	evicted := ContractFacts{Ledger: 20, Changes: []Change{{Kind: Evicted, Key: &key}}}
	applyFacts(t, ix, evicted)
	var count, archived int
	if err := s.DB.QueryRow(`SELECT count(*) FROM claims WHERE current = 1`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("archived reference claims %d: %v", count, err)
	}
	if err := s.DB.QueryRow(`SELECT archived FROM contracts`).Scan(&archived); err != nil || archived != 1 {
		t.Fatalf("archived %d: %v", archived, err)
	}
	restored := refChange(t, owner, "v1", h)
	restored.Kind = Restored
	final := ContractFacts{Ledger: 30, Changes: []Change{restored}}
	applyFacts(t, ix, final)
	if err := s.DB.QueryRow(`SELECT count(*) FROM claims WHERE current = 1`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("restored claims %d: %v", count, err)
	}
	before := dump(t, s)
	applyFacts(t, ix, initial, evicted, final)
	if after := dump(t, s); before != after {
		t.Fatalf("archival replay changed tables:\n%s\n%s", before, after)
	}
}

func TestReferenceFallbackNeverUsesFutureState(t *testing.T) {
	code, h := wasmFixture(t, "token_full_sep.wasm")
	owner := seedCID(9)
	change := refChange(t, owner, "v1", h)
	key, _ := ExecRefKey(owner, "v1")
	ix, s := newIndexer(t, &entryRPC{t: t, latest: 30, code: map[string][]byte{h: code}, data: map[string]xdr.ContractDataEntry{key: *change.Entry.Data.ContractData}, lastMod: map[string]uint32{key: 20}})
	applyFacts(t, ix, ContractFacts{Ledger: 10, Changes: []Change{referenceInstance(t, seedCID(1), owner, "v1")}})
	var hash *string
	if err := s.DB.QueryRow(`SELECT current_wasm_hash FROM contracts`).Scan(&hash); err != nil || hash != nil {
		t.Fatalf("future reference used: %v, %v", hash, err)
	}
	applyFacts(t, ix, ContractFacts{Ledger: 20, Changes: []Change{change}})
	if err := s.DB.QueryRow(`SELECT current_wasm_hash FROM contracts`).Scan(&hash); err != nil || hash == nil || *hash != h {
		t.Fatalf("reference not resolved at change: %v, %v", hash, err)
	}
}
