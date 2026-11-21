package ingest

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func TestCheckerReportsDifferences(t *testing.T) {
	ctx := context.Background()
	h := fixtureWasmHash(t, "token_full_sep.wasm")
	h2 := fixtureWasmHash(t, "token_partial.wasm")
	owner := seedCID(9)
	tag := "shared-v1"
	okID, staleID, refID, sacID, absentID, archivedID, changedID, expiredID := seedCID(1), seedCID(2), seedCID(3), seedCID(4), seedCID(5), seedCID(6), seedCID(7), seedCID(8)

	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "check.db"), store.Options{Passphrase: "Test SDF Network ; September 2015"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows := []store.ContractRow{
		{ID: okID, Kind: "wasm", CurrentWasmHash: h, UpdatedLedger: 100},
		{ID: staleID, Kind: "wasm", CurrentWasmHash: h, UpdatedLedger: 100},
		{ID: refID, Kind: "wasm_ref", CurrentWasmHash: h, ExecRefOwner: owner, ExecRefTag: tag, UpdatedLedger: 100},
		{ID: sacID, Kind: "sac", SACAsset: "native", UpdatedLedger: 100},
		{ID: absentID, Kind: "wasm", CurrentWasmHash: h, UpdatedLedger: 100},
		{ID: archivedID, Kind: "wasm", CurrentWasmHash: h, UpdatedLedger: 100, Archived: true},
		{ID: changedID, Kind: "wasm", CurrentWasmHash: h, UpdatedLedger: 100},
		{ID: expiredID, Kind: "wasm", CurrentWasmHash: h, UpdatedLedger: 100},
	}
	for _, row := range rows {
		if err := tx.UpsertContract(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.SetState(ctx, store.KeyLastLedger, "100"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	data := map[string]xdr.ContractDataEntry{}
	lastMod := map[string]uint32{}
	expired := map[string]bool{}
	addInstance := func(id string, ex xdr.ContractExecutable) string {
		cd := instanceEntry(t, ex, nil)
		cd.Contract = contractAddr(t, id)
		key, err := InstanceKey(id)
		if err != nil {
			t.Fatal(err)
		}
		data[key] = cd
		return key
	}
	addInstance(okID, execWasm(h))
	key := addInstance(staleID, execWasm(h2))
	lastMod[key] = 100
	addInstance(refID, xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableExternalRef, ExternalRef: &xdr.ContractExecutableExternalRef{ExecutableOwner: contractAddr(t, owner), Tag: xdr.ScString(tag)}})
	refHash, err := ParseWasmHash(h2)
	if err != nil {
		t.Fatal(err)
	}
	refBytes := xdr.ScBytes(refHash[:])
	refEntry := xdr.ContractDataEntry{Contract: contractAddr(t, owner), Durability: xdr.ContractDataDurabilityPersistent,
		Key: xdr.ScVal{Type: xdr.ScValTypeScvExecutableTag, ExecutableTag: func() *xdr.ScString { s := xdr.ScString(tag); return &s }()},
		Val: xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &refBytes}}
	rk, err := ExecRefKey(owner, tag)
	if err != nil {
		t.Fatal(err)
	}
	data[rk] = refEntry
	lastMod[rk] = 100
	addInstance(sacID, execWasm(h))
	key = addInstance(changedID, execWasm(h2))
	lastMod[key] = 110
	key = addInstance(expiredID, execWasm(h2))
	expired[key] = true

	checker := &Checker{Store: s, RPC: &entryRPC{t: t, latest: 120, data: data, lastMod: lastMod, expired: expired}}
	res, err := checker.Check(ctx, CheckOptions{Sample: 100, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sampled != len(rows) || res.LastLedger != 100 || res.LatestLedger != 120 {
		t.Fatalf("result = %+v", res)
	}
	if res.ChangedAfterIndex != 1 {
		t.Fatalf("changed after index = %d, want 1", res.ChangedAfterIndex)
	}
	if res.Archived != 1 {
		t.Fatalf("archived = %d, want 1", res.Archived)
	}
	want := map[string]string{
		staleID + "/wasm_hash":  h2,
		refID + "/wasm_hash":    h2,
		sacID + "/kind":         "wasm",
		absentID + "/instance":  "absent",
		expiredID + "/archived": "true",
	}
	for _, d := range res.Differences {
		if _, ok := want[d.ContractID+"/"+d.Field]; ok {
			delete(want, d.ContractID+"/"+d.Field)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing differences %v in %+v", want, res.Differences)
	}
	for _, d := range res.Differences {
		if d.ContractID == changedID || d.ContractID == archivedID || d.ContractID == okID {
			t.Fatalf("unexpected difference %+v", d)
		}
	}
}

func TestCheckerUnresolvedReference(t *testing.T) {
	ctx := context.Background()
	id, owner := seedCID(11), seedCID(12)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "check.db"), store.Options{Passphrase: "Test SDF Network ; September 2015"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.UpsertContract(ctx, store.ContractRow{ID: id, Kind: "wasm_ref", ExecRefOwner: owner, ExecRefTag: "missing", UpdatedLedger: 100}); err != nil {
		t.Fatal(err)
	}
	if err := tx.SetState(ctx, store.KeyLastLedger, "100"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	cd := instanceEntry(t, xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableExternalRef, ExternalRef: &xdr.ContractExecutableExternalRef{ExecutableOwner: contractAddr(t, owner), Tag: xdr.ScString("missing")}}, nil)
	cd.Contract = contractAddr(t, id)
	key, err := InstanceKey(id)
	if err != nil {
		t.Fatal(err)
	}
	res, err := (&Checker{Store: s, RPC: &entryRPC{t: t, latest: 120, data: map[string]xdr.ContractDataEntry{key: cd}}}).Check(ctx, CheckOptions{Sample: 10, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Differences) != 1 || res.Differences[0].Field != "wasm_hash" || res.Differences[0].RPC != "unresolved" {
		t.Fatalf("differences = %+v", res.Differences)
	}
}

func TestCheckerRequiresIndexedLedger(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "check.db"), store.Options{Passphrase: "Test SDF Network ; September 2015"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	_, err = (&Checker{Store: s, RPC: &entryRPC{t: t}}).Check(context.Background(), CheckOptions{Sample: 1, Seed: 1})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := (&Checker{Store: s, RPC: &entryRPC{t: t}}).Check(context.Background(), CheckOptions{Sample: 0}); err == nil {
		t.Fatal("zero sample accepted")
	}
}

func fixtureWasmHash(t *testing.T, name string) string {
	t.Helper()
	_, h := wasmFixture(t, name)
	return h
}
