package ingest

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

const nativeSAC = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"

func TestDecodeRecordedNativeSAC(t *testing.T) {
	b, err := os.ReadFile("testdata/sac_native_instance.json")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			Entries []struct {
				XDR string `json:"xdr"`
			} `json:"entries"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	data, err := DecodeLedgerEntryDataBase64(resp.Result.Entries[0].XDR, XDRLimits{})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := DecodeInstance(*data.ContractData)
	if err != nil {
		t.Fatal(err)
	}
	if inst.ContractID != nativeSAC || inst.Kind != KindSAC || inst.SACAsset != "native" || inst.WasmHash != "" {
		t.Fatalf("got %+v", inst)
	}
}

func contractAddr(t *testing.T, id string) xdr.ScAddress {
	t.Helper()
	a, err := contractAddress(id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func instanceEntry(t *testing.T, ex xdr.ContractExecutable, storage *xdr.ScMap) xdr.ContractDataEntry {
	return xdr.ContractDataEntry{
		Contract: contractAddr(t, nativeSAC), Durability: xdr.ContractDataDurabilityPersistent,
		Key: xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
		Val: xdr.ScVal{Type: xdr.ScValTypeScvContractInstance, Instance: &xdr.ScContractInstance{Executable: ex, Storage: storage}},
	}
}

func TestDecodeInstance(t *testing.T) {
	var h xdr.Hash
	h[0], h[31] = 0xab, 0x01
	tag := xdr.ScString("v1")
	owner := contractAddr(t, nativeSAC)
	tests := []struct {
		name    string
		entry   xdr.ContractDataEntry
		want    Instance
		wantErr error
	}{
		{
			"wasm executable yields its hash",
			instanceEntry(t, xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &h}, nil),
			Instance{ContractID: nativeSAC, Kind: KindWasm, WasmHash: "ab" + strings.Repeat("00", 30) + "01"}, nil,
		},
		{
			"external reference yields owner and tag (CAP-85)",
			instanceEntry(t, xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableExternalRef,
				ExternalRef: &xdr.ContractExecutableExternalRef{ExecutableOwner: owner, Tag: tag}}, nil),
			Instance{ContractID: nativeSAC, Kind: KindWasmRef, RefOwner: nativeSAC, RefTag: "v1"}, nil,
		},
		{
			"SAC without metadata has no asset name, not a guess",
			instanceEntry(t, xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableStellarAsset}, nil),
			Instance{ContractID: nativeSAC, Kind: KindSAC}, nil,
		},
		{
			"temporary durability is not an instance",
			func() xdr.ContractDataEntry {
				e := instanceEntry(t, xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &h}, nil)
				e.Durability = xdr.ContractDataDurabilityTemporary
				return e
			}(),
			Instance{}, ErrNotInstance,
		},
		{
			"wasm executable without a hash is malformed",
			instanceEntry(t, xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm}, nil),
			Instance{}, ErrMalformed,
		},
		{
			"instance key whose value is not an instance is malformed",
			xdr.ContractDataEntry{Contract: owner, Durability: xdr.ContractDataDurabilityPersistent,
				Key: xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance}, Val: xdr.ScVal{Type: xdr.ScValTypeScvVoid}},
			Instance{}, ErrMalformed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeInstance(tt.entry)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestDecodeExecRef(t *testing.T) {
	tag := xdr.ScString("pool-v2")
	hash := make([]byte, 32)
	hash[0] = 0xcd
	entry := func(val xdr.ScVal, dur xdr.ContractDataDurability) xdr.ContractDataEntry {
		return xdr.ContractDataEntry{Contract: contractAddr(t, nativeSAC), Durability: dur,
			Key: xdr.ScVal{Type: xdr.ScValTypeScvExecutableTag, ExecutableTag: &tag}, Val: val}
	}
	bytesVal := func(b []byte) xdr.ScVal {
		s := xdr.ScBytes(b)
		return xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &s}
	}

	got, err := DecodeExecRef(entry(bytesVal(hash), xdr.ContractDataDurabilityPersistent))
	if err != nil || got.Owner != nativeSAC || got.Tag != "pool-v2" || got.WasmHash != "cd"+strings.Repeat("00", 31) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := DecodeExecRef(entry(bytesVal(hash[:31]), xdr.ContractDataDurabilityPersistent)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("31-byte hash: err = %v, want ErrMalformed", err)
	}
	if _, err := DecodeExecRef(entry(bytesVal(hash), xdr.ContractDataDurabilityTemporary)); !errors.Is(err, ErrNotExecRef) {
		t.Fatalf("temporary: err = %v, want ErrNotExecRef", err)
	}
}

func TestKeysRoundTrip(t *testing.T) {
	k, err := InstanceKey(nativeSAC)
	if err != nil || k != "AAAABgAAAAHXkotywnA8z+r365/0701QSlWouXn8m0UOoshCtNHOYQAAABQAAAAB" {
		t.Fatalf("instance key = %q, %v (the key used to record testdata/sac_native_instance.json)", k, err)
	}
	if _, err := ContractCodeKey(strings.Repeat("ab", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := ExecRefKey(nativeSAC, "v1"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", strings.Repeat("AB", 32), strings.Repeat("a", 63), strings.Repeat("g", 64)} {
		if _, err := ParseWasmHash(bad); err == nil {
			t.Errorf("ParseWasmHash(%q) accepted", bad)
		}
	}
	if _, err := InstanceKey("GABC"); err == nil {
		t.Error("InstanceKey accepted a non-contract strkey")
	}
}

func TestUnmarshalBase64Limits(t *testing.T) {
	var cd xdr.ContractDataEntry
	if err := UnmarshalBase64(strings.Repeat("A", 100), &cd, XDRLimits{MaxBase64Len: 99}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if err := UnmarshalBase64("!!!!", &cd, XDRLimits{}); err == nil {
		t.Fatal("invalid base64 accepted")
	}
}
