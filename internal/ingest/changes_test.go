package ingest

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// recordedLedgers loads the real testnet ledgers recorded for the rpc
// package (internal/rpc/testdata/README.md).
func recordedLedgers(t *testing.T) []xdr.LedgerCloseMeta {
	t.Helper()
	b, err := os.ReadFile("../rpc/testdata/getLedgers.json")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			Ledgers []struct {
				Sequence    uint32 `json:"sequence"`
				MetadataXDR string `json:"metadataXdr"`
			} `json:"ledgers"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	var out []xdr.LedgerCloseMeta
	for _, l := range resp.Result.Ledgers {
		var m xdr.LedgerCloseMeta
		if err := UnmarshalBase64(l.MetadataXDR, &m, XDRLimits{MaxBase64Len: 8 << 20}); err != nil {
			t.Fatal(err)
		}
		if m.LedgerSequence() != l.Sequence {
			t.Fatalf("meta for %d decodes as %d", l.Sequence, m.LedgerSequence())
		}
		out = append(out, m)
	}
	return out
}

func TestRecordedTestnetLedgers(t *testing.T) {
	for _, m := range recordedLedgers(t) {
		changes, err := Changes(m)
		if err != nil {
			t.Fatalf("ledger %d: %v", m.LedgerSequence(), err)
		}
		f, err := ExtractContractFacts(m)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("ledger %d: meta v%d, %d txs, %d changes; code created %d, instances %d, exec refs %d, evicted %d, skipped %d",
			m.LedgerSequence(), m.V, m.CountTransactions(), len(changes), len(f.CodeCreated), len(f.Instances), len(f.ExecRefs), len(f.Evicted), len(f.Skipped))
		if len(f.Skipped) != 0 {
			t.Fatalf("real ledger had undecodable instance entries: %v", f.Skipped)
		}
		for _, in := range f.Instances {
			if in.ContractID == "" || in.Kind == "" || (in.Kind == KindWasm && len(in.WasmHash) != 64) {
				t.Fatalf("bad instance %+v", in)
			}
		}
	}
}

func entryChange(kind xdr.LedgerEntryChangeType, e xdr.LedgerEntry) xdr.LedgerEntryChange {
	c := xdr.LedgerEntryChange{Type: kind}
	switch kind {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		c.Created = &e
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		c.Updated = &e
	case xdr.LedgerEntryChangeTypeLedgerEntryState:
		c.State = &e
	case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		c.Restored = &e
	}
	return c
}

func codeEntry(b byte) xdr.LedgerEntry {
	var h xdr.Hash
	h[0] = b
	return xdr.LedgerEntry{Data: xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.ContractCodeEntry{Hash: h}}}
}

// TestChangesApplyOrder builds a v2 ledger with one code entry in every slot
// (marked by its first hash byte) and checks they come out in apply order.
func TestChangesApplyOrder(t *testing.T) {
	created := xdr.LedgerEntryChangeTypeLedgerEntryCreated
	tx := xdr.TransactionResultMetaV1{
		FeeProcessing: xdr.LedgerEntryChanges{entryChange(created, codeEntry(1))},
		TxApplyProcessing: xdr.TransactionMeta{V: 4, V4: &xdr.TransactionMetaV4{
			TxChangesBefore: xdr.LedgerEntryChanges{entryChange(created, codeEntry(2))},
			Operations: []xdr.OperationMetaV2{
				{Changes: xdr.LedgerEntryChanges{
					entryChange(xdr.LedgerEntryChangeTypeLedgerEntryState, codeEntry(99)), // pre-image: not reported
					entryChange(created, codeEntry(3)),
				}},
				{Changes: xdr.LedgerEntryChanges{entryChange(xdr.LedgerEntryChangeTypeLedgerEntryRestored, codeEntry(4))}},
			},
			TxChangesAfter: xdr.LedgerEntryChanges{entryChange(created, codeEntry(5))},
		}},
		PostTxApplyFeeProcessing: xdr.LedgerEntryChanges{entryChange(created, codeEntry(6))},
	}
	var evictKey xdr.LedgerKey
	evictKey.Type = xdr.LedgerEntryTypeContractCode
	evictKey.ContractCode = &xdr.LedgerKeyContractCode{}
	m := xdr.LedgerCloseMeta{V: 2, V2: &xdr.LedgerCloseMetaV2{
		TxProcessing:       []xdr.TransactionResultMetaV1{tx},
		UpgradesProcessing: []xdr.UpgradeEntryMeta{{Changes: xdr.LedgerEntryChanges{entryChange(created, codeEntry(7))}}},
		EvictedKeys:        []xdr.LedgerKey{evictKey},
	}}
	changes, err := Changes(m)
	if err != nil {
		t.Fatal(err)
	}
	var order []byte
	for _, c := range changes {
		if c.Kind == Evicted {
			order = append(order, 0xee)
			continue
		}
		order = append(order, c.Entry.Data.ContractCode.Hash[0])
	}
	if string(order) != string([]byte{1, 2, 3, 4, 5, 6, 7, 0xee}) {
		t.Fatalf("order = %v", order)
	}
	f, err := ExtractContractFacts(m)
	if err != nil || len(f.CodeCreated) != 7 || len(f.Evicted) != 1 {
		t.Fatalf("facts = %+v, %v", f, err)
	}
}

func TestExtractContractFactsInstancesAndRefs(t *testing.T) {
	var h xdr.Hash
	h[0] = 0xaa
	inst := instanceEntry(t, xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &h}, nil)
	tag := xdr.ScString("t")
	hb := xdr.ScBytes(make([]byte, 32))
	ref := xdr.ContractDataEntry{Contract: contractAddr(t, nativeSAC), Durability: xdr.ContractDataDurabilityPersistent,
		Key: xdr.ScVal{Type: xdr.ScValTypeScvExecutableTag, ExecutableTag: &tag}, Val: xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &hb}}
	badRef := ref
	short := xdr.ScBytes(make([]byte, 3))
	badRef.Val = xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &short}
	data := func(cd xdr.ContractDataEntry) xdr.LedgerEntry {
		return xdr.LedgerEntry{Data: xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractData, ContractData: &cd}}
	}
	m := xdr.LedgerCloseMeta{V: 1, V1: &xdr.LedgerCloseMetaV1{TxProcessing: []xdr.TransactionResultMeta{{
		TxApplyProcessing: xdr.TransactionMeta{V: 3, V3: &xdr.TransactionMetaV3{Operations: []xdr.OperationMeta{{Changes: xdr.LedgerEntryChanges{
			entryChange(xdr.LedgerEntryChangeTypeLedgerEntryCreated, data(inst)),
			entryChange(xdr.LedgerEntryChangeTypeLedgerEntryUpdated, data(inst)),
			entryChange(xdr.LedgerEntryChangeTypeLedgerEntryCreated, data(ref)),
			entryChange(xdr.LedgerEntryChangeTypeLedgerEntryUpdated, data(badRef)),
		}}}}},
	}}}}
	f, err := ExtractContractFacts(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Instances) != 2 || !f.Instances[0].Created || f.Instances[1].Created {
		t.Fatalf("instances = %+v", f.Instances)
	}
	if len(f.ExecRefs) != 1 || len(f.Skipped) != 1 || !errors.Is(f.Skipped[0], ErrMalformed) {
		t.Fatalf("refs %+v skipped %v", f.ExecRefs, f.Skipped)
	}
}

func TestUnsupportedMetaVersions(t *testing.T) {
	if _, err := Changes(xdr.LedgerCloseMeta{V: 3}); !errors.Is(err, ErrUnsupportedMeta) {
		t.Fatalf("LedgerCloseMeta v3: err = %v", err)
	}
	m := xdr.LedgerCloseMeta{V: 1, V1: &xdr.LedgerCloseMetaV1{TxProcessing: []xdr.TransactionResultMeta{{TxApplyProcessing: xdr.TransactionMeta{V: 9}}}}}
	if _, err := Changes(m); !errors.Is(err, ErrUnsupportedMeta) {
		t.Fatalf("TransactionMeta v9: err = %v", err)
	}
	if _, err := Changes(xdr.LedgerCloseMeta{V: 2}); !errors.Is(err, ErrUnsupportedMeta) {
		t.Fatalf("v2 without body: err = %v", err)
	}
}
